package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/HarshShah0203/homedex/internal/domain"
	"github.com/HarshShah0203/homedex/internal/store"
)

type storedHost struct {
	id                   int64
	name, address, power string
	aliases              string
	parent               sql.NullInt64
}

func readHost(t *testing.T, st *store.Store, key string) storedHost {
	t.Helper()
	var h storedHost
	if err := st.DB().QueryRow(`SELECT id,name,address,aliases,power_state,parent_host_id FROM hosts WHERE natural_key=?`, key).Scan(&h.id, &h.name, &h.address, &h.aliases, &h.power, &h.parent); err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return h
}

func lastHostDiff(t *testing.T, st *store.Store, id int64) map[string]map[string]string {
	t.Helper()
	var raw string
	if err := st.DB().QueryRow(`SELECT diff FROM changes WHERE entity_type='host' AND entity_id=? AND change_kind='modified' ORDER BY id DESC LIMIT 1`, id).Scan(&raw); err != nil {
		t.Fatalf("no modified change for host %d: %v", id, err)
	}
	var diff map[string]map[string]string
	if err := json.Unmarshal([]byte(raw), &diff); err != nil {
		t.Fatal(err)
	}
	return diff
}

func TestApplyHostParentsAndPowerState(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	id, err := st.CreateConnector(ctx, "proxmox", "Proxmox", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, nil)
	apply := func(hosts ...domain.Host) int {
		t.Helper()
		_, n, err := a.Apply(ctx, id, domain.Snapshot{Hosts: hosts})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	node := func(name string) domain.Host {
		return domain.Host{Key: "node:" + name, Name: name, Kind: domain.HostKindProxmoxNode, PowerState: "online"}
	}
	web := domain.Host{Key: "qemu:101", Name: "web", Kind: domain.HostKindVM, Address: "192.0.2.21", PowerState: "running", ParentKey: "node:pve1"}

	// The guest comes first in the snapshot; its parent is still linked.
	if n := apply(web, node("pve1"), node("pve2")); n != 3 {
		t.Fatalf("first scan changes=%d, want 3", n)
	}
	pve1, pve2 := readHost(t, st, "node:pve1"), readHost(t, st, "node:pve2")
	got := readHost(t, st, "qemu:101")
	if !got.parent.Valid || got.parent.Int64 != pve1.id || got.power != "running" || pve1.power != "online" || pve1.parent.Valid {
		t.Fatalf("stored guest=%+v node=%+v", got, pve1)
	}
	if n := apply(web, node("pve1"), node("pve2")); n != 0 {
		t.Fatalf("unchanged rescan changes=%d, want 0", n)
	}

	// Migration to another node is one change on the same row.
	web.ParentKey = "node:pve2"
	if n := apply(web, node("pve1"), node("pve2")); n != 1 {
		t.Fatalf("migration changes=%d, want 1", n)
	}
	moved := readHost(t, st, "qemu:101")
	if moved.id != got.id || moved.parent.Int64 != pve2.id {
		t.Fatalf("after migration %+v, want id %d on %d", moved, got.id, pve2.id)
	}
	if diff := lastHostDiff(t, st, moved.id); diff["parent"]["before"] != "pve1" || diff["parent"]["after"] != "pve2" || len(diff) != 1 {
		t.Fatalf("migration diff = %v", diff)
	}

	// Stopping files the power change and the addresses it no longer has.
	web.PowerState, web.Address = "stopped", ""
	if n := apply(web, node("pve1"), node("pve2")); n != 1 {
		t.Fatalf("stop changes=%d, want 1", n)
	}
	if diff := lastHostDiff(t, st, moved.id); diff["power"]["before"] != "running" || diff["power"]["after"] != "stopped" || diff["address"]["before"] != "192.0.2.21" {
		t.Fatalf("stop diff = %v", diff)
	}

	// A node the snapshot no longer lists leaves the guest without a parent.
	if n := apply(web, node("pve1")); n != 2 {
		t.Fatalf("node removal changes=%d, want the node gone and the guest's parent cleared", n)
	}
	if orphan := readHost(t, st, "qemu:101"); orphan.parent.Valid {
		t.Fatalf("parent kept after its node left the snapshot: %+v", orphan)
	}
}

func TestApplyHostKeepsFactsTheSourceCouldNotRead(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	id, err := st.CreateConnector(ctx, "proxmox", "Proxmox", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, nil)
	apply := func(hosts ...domain.Host) int {
		t.Helper()
		_, n, err := a.Apply(ctx, id, domain.Snapshot{Hosts: hosts})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	web := domain.Host{Key: "qemu:101", Name: "web", Kind: domain.HostKindVM, Address: "192.0.2.21", Aliases: []string{"198.51.100.21"}, PowerState: "running"}
	apply(web)

	// The agent did not answer and the node lost the guest's name: nothing the
	// scan could not read is overwritten, so nothing is filed.
	unread := web
	unread.Name, unread.NameUnread = "VM 101", true
	unread.Address, unread.Aliases, unread.AddressesUnread = "", nil, true
	if n := apply(unread); n != 0 {
		t.Fatalf("unread facts filed %d changes", n)
	}
	kept := readHost(t, st, "qemu:101")
	if kept.name != "web" || kept.address != "192.0.2.21" || kept.aliases != `["198.51.100.21"]` {
		t.Fatalf("unread facts overwritten: %+v", kept)
	}

	// What the scan could read still updates.
	unread.PowerState = "paused"
	if n := apply(unread); n != 1 {
		t.Fatalf("power change beside unread facts filed %d changes, want 1", n)
	}
	if diff := lastHostDiff(t, st, kept.id); len(diff) != 1 || diff["power"]["after"] != "paused" {
		t.Fatalf("diff = %v, want only power", diff)
	}

	// A host seen for the first time is stored as reported.
	fresh := domain.Host{Key: "lxc:200", Name: "CT 200", NameUnread: true, Kind: domain.HostKindLXC, AddressesUnread: true, PowerState: "unknown"}
	if n := apply(unread, fresh); n != 1 {
		t.Fatalf("new unread host changes=%d, want 1", n)
	}
	if got := readHost(t, st, "lxc:200"); got.name != "CT 200" || got.address != "" || got.aliases != "[]" || got.power != "unknown" {
		t.Fatalf("new host stored as %+v", got)
	}
}
