package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/imageref"
)

// imageService is a container as update checks see it.
type imageService struct {
	image, tag  string
	repoDigests []string
}

// imageCheckedServices selects, as services s, the containers update checks
// cover: still in the inventory, naming an image, and found by a source of
// kind imageref.SourceKind (the one query argument).
const imageCheckedServices = `s.state!='gone' AND s.kind='container' AND s.image!='' AND s.connector_id IN (SELECT id FROM connectors WHERE kind=?)`

// loadImageServices groups the containers update checks cover by the
// reference they name, the same key image_updates rows use.
func loadImageServices(ctx context.Context, tx *sql.Tx) (map[string][]imageService, error) {
	rows, err := tx.QueryContext(ctx, `SELECT s.image,s.tag,s.repo_digests FROM services s WHERE `+imageCheckedServices+` ORDER BY s.id`, imageref.SourceKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]imageService{}
	for rows.Next() {
		var svc imageService
		var digests string
		if err = rows.Scan(&svc.image, &svc.tag, &digests); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(digests), &svc.repoDigests)
		ref := imageref.Join(svc.image, svc.tag)
		out[ref] = append(out[ref], svc)
	}
	return out, rows.Err()
}

// applyImageUpdates stores one registry lookup per image reference and files a
// single change-feed entry the first time a remote digest is seen that some
// running container is behind. The entry is keyed to the reference, not the
// containers, so a tag run by five containers produces one entry, and a
// lookup that returns the same digest again produces none. Digests that did
// not change never reach the change feed.
func applyImageUpdates(ctx context.Context, tx *sql.Tx, connectorID, runID int64, now string, items []domain.ImageUpdate) (int, error) {
	sorted := append([]domain.ImageUpdate(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Ref < sorted[j].Ref })
	seen := map[string]bool{}
	changes := 0
	var services map[string][]imageService
	for _, u := range sorted {
		ref := strings.TrimSpace(u.Ref)
		if ref == "" {
			return 0, fmt.Errorf("image reference is required")
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		lookup, remote, reason := u.Lookup, strings.TrimSpace(u.RemoteDigest), strings.TrimSpace(u.Reason)
		switch lookup {
		case imageref.LookupResolved:
			if !imageref.ValidDigest(remote) {
				lookup, remote, reason = imageref.LookupUnknown, "", "The registry returned a malformed digest."
			}
		case imageref.LookupPinned:
			remote = ""
		default:
			lookup, remote = imageref.LookupUnknown, ""
		}
		checked := now
		if !u.CheckedAt.IsZero() {
			checked = u.CheckedAt.UTC().Format(time.RFC3339Nano)
		}
		var id int64
		var oldLookup, oldRemote, oldChecked, notified string
		err := tx.QueryRowContext(ctx, `SELECT id,lookup,remote_digest,checked_at,notified_digest FROM image_updates WHERE image_ref=?`, ref).Scan(&id, &oldLookup, &oldRemote, &oldChecked, &notified)
		switch {
		case err == sql.ErrNoRows:
			res, e := tx.ExecContext(ctx, `INSERT INTO image_updates(connector_id,image_ref,lookup,remote_digest,reason,checked_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, connectorID, ref, lookup, remote, reason, checked, now, now)
			if e != nil {
				return 0, e
			}
			id, _ = res.LastInsertId()
		case err != nil:
			return 0, err
		default:
			// A rate limit or outage says nothing new about the tag: keep the last
			// answer (and when it was obtained) rather than flapping to unknown.
			if u.Transient && lookup == imageref.LookupUnknown && oldLookup == imageref.LookupResolved {
				lookup, remote, checked = oldLookup, oldRemote, oldChecked
				if reason != "" {
					reason = "Last check failed: " + reason
				}
			}
			if _, err = tx.ExecContext(ctx, `UPDATE image_updates SET connector_id=?,lookup=?,remote_digest=?,reason=?,checked_at=?,retired_at=NULL,updated_at=? WHERE id=?`, connectorID, lookup, remote, reason, checked, now, id); err != nil {
				return 0, err
			}
		}
		if lookup != imageref.LookupResolved || remote == notified {
			continue
		}
		if services == nil {
			if services, err = loadImageServices(ctx, tx); err != nil {
				return 0, err
			}
		}
		var behind []string
		for _, svc := range services[ref] {
			result := imageref.Compare(svc.image, svc.tag, svc.repoDigests, imageref.Lookup{Outcome: lookup, RemoteDigest: remote})
			if result.Status == imageref.UpdateAvailable {
				behind = append(behind, result.Running)
			}
		}
		if len(behind) == 0 {
			continue
		}
		diff := map[string]any{"digest": map[string]string{"before": imageref.Short(behind[0]), "after": imageref.Short(remote)}, "services": len(behind)}
		if err = addChange(ctx, tx, runID, "image", id, "modified", "Update available for "+ref, diff, now); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE image_updates SET notified_digest=? WHERE id=?`, remote, id); err != nil {
			return 0, err
		}
		changes++
	}
	// A reference no container runs any more (or one this source now skips) has
	// nothing to report, so its row is retired: the services API ignores it.
	// It is kept, with the digest the change feed last reported, until
	// PurgeGone's retention passes, so a container recreated on the same old
	// image does not file the same update a second time.
	rows, err := tx.QueryContext(ctx, `SELECT id,image_ref FROM image_updates WHERE connector_id=? AND retired_at IS NULL`, connectorID)
	if err != nil {
		return 0, err
	}
	var stale []int64
	for rows.Next() {
		var id int64
		var ref string
		if err = rows.Scan(&id, &ref); err != nil {
			rows.Close()
			return 0, err
		}
		if !seen[ref] {
			stale = append(stale, id)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range stale {
		if _, err = tx.ExecContext(ctx, `UPDATE image_updates SET retired_at=?,updated_at=? WHERE id=?`, now, now, id); err != nil {
			return 0, err
		}
	}
	return changes, nil
}

// purgeRetiredImageUpdates deletes lookups retired before cutoff.
func purgeRetiredImageUpdates(ctx context.Context, tx *sql.Tx, cutoff string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM image_updates WHERE retired_at IS NOT NULL AND retired_at < ?`, cutoff)
	return err
}
