package docker

import (
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

// containerJSON builds the minimum inspect payload mapContainer reads for
// identity: the container name plus whatever compose labels are in play.
func containerJSON(id, name string, labels map[string]string) types.ContainerJSON {
	return types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{ID: id, Name: "/" + name, State: &types.ContainerState{Status: "running"}},
		Config:            &container.Config{Image: "ghcr.io/immich-app/immich-server:v1.0", Labels: labels},
	}
}

// A recreate -- `compose up` after an image bump, or after a `down` -- gives the
// container a brand new ID while reusing its name. The natural key has to follow
// the name, or the service row is orphaned along with its notes and first_seen.
func TestMapContainerKeyIsStableAcrossRecreate(t *testing.T) {
	labels := map[string]string{"com.docker.compose.project": "media", "com.docker.compose.service": "immich"}
	before, _ := mapContainer("docker:nas", types.Container{ID: "aaa111"}, containerJSON("aaa111", "media-immich-1", labels))
	after, _ := mapContainer("docker:nas", types.Container{ID: "bbb222"}, containerJSON("bbb222", "media-immich-1", labels))

	if before.Key != after.Key {
		t.Fatalf("key moved across recreate: %q -> %q", before.Key, after.Key)
	}
	if before.Key != "docker:nas:media-immich-1" {
		t.Fatalf("key = %q, want it built from the host and container name", before.Key)
	}
	// The compose service name is still the nicer label, so it stays on display.
	if before.Name != "immich" {
		t.Fatalf("display name = %q, want the compose service name", before.Name)
	}
}

// `docker compose up --scale web=3` gives three live containers an identical
// com.docker.compose.service label. Keying on it would collapse them into one
// row that flaps between their states.
func TestMapContainerKeysScaledReplicasDistinctly(t *testing.T) {
	labels := map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.service": "web"}
	keys := map[string]bool{}
	for _, name := range []string{"shop-web-1", "shop-web-2", "shop-web-3"} {
		svc, _ := mapContainer("docker:nas", types.Container{ID: "id-" + name}, containerJSON("id-"+name, name, labels))
		keys[svc.Key] = true
	}
	if len(keys) != 3 {
		t.Fatalf("scaled replicas collapsed to %d key(s): %v", len(keys), keys)
	}
}

// A hand-run container has no compose labels at all; its own name is the identity.
func TestMapContainerKeysUnlabelledContainerByName(t *testing.T) {
	svc, _ := mapContainer("docker:nas", types.Container{ID: "ccc333"}, containerJSON("ccc333", "pihole", nil))
	if svc.Key != "docker:nas:pihole" || svc.Name != "pihole" {
		t.Fatalf("key=%q name=%q", svc.Key, svc.Name)
	}
}

// Nameless containers must still get a unique key rather than colliding on "".
func TestMapContainerFallsBackToIDWhenNameless(t *testing.T) {
	in := containerJSON("ddd444", "", nil)
	in.Name = ""
	svc, _ := mapContainer("docker:nas", types.Container{ID: "ddd444"}, in)
	if svc.Key != "docker:nas:ddd444" {
		t.Fatalf("key = %q, want the ID as fallback", svc.Key)
	}
}
