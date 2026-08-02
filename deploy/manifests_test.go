// Package deploy holds no code. This test guards the manifests in this
// directory against regressions that only a live apiserver would otherwise
// catch — and CI has no cluster.
//
// Every assertion here corresponds to a defect that was actually shipped in
// these files and found by applying them to a real Kubernetes cluster. They
// are cheap, offline, and they fail loudly.
package deploy

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// manifests returns each Kubernetes YAML in deploy/k8s.
func manifests(t *testing.T) map[string]string {
	t.Helper()
	paths, err := filepath.Glob("k8s/*.yaml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no manifests found under deploy/k8s (err=%v)", err)
	}
	out := map[string]string{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out[p] = string(b)
	}
	return out
}

// PodSecurity "restricted" rejects a pod with no seccompProfile. Both
// manifests shipped without one and a real apiserver warned on both; on a
// cluster that ENFORCES restricted rather than warns, the pod never starts.
//
// Asserted as plain text rather than by parsing: the point is that the field
// is present and greppable, and a YAML round-trip in a test would not have
// caught the original omission any better.
func TestManifestsSetSeccompProfile(t *testing.T) {
	for path, body := range manifests(t) {
		if !strings.Contains(body, "seccompProfile") {
			t.Errorf("%s: no seccompProfile — PodSecurity \"restricted\" rejects this pod", path)
		}
		if !strings.Contains(body, "RuntimeDefault") {
			t.Errorf("%s: seccompProfile must be RuntimeDefault or Localhost", path)
		}
	}
}

// Every container needs capabilities.drop: ["ALL"] under restricted. The
// autossh sidecar shipped without it.
func TestEveryContainerDropsAllCapabilities(t *testing.T) {
	for path, body := range manifests(t) {
		checked := 0
		for _, doc := range decodeAll(t, path, body) {
			for _, c := range containersOf(doc) {
				checked++
				name, _ := c["name"].(string)
				sc, _ := c["securityContext"].(map[string]any)
				caps, _ := sc["capabilities"].(map[string]any)
				drop, _ := caps["drop"].([]any)
				if len(drop) != 1 || drop[0] != "ALL" {
					t.Errorf("%s: container %q does not drop ALL capabilities — "+
						"PodSecurity \"restricted\" requires it", path, name)
				}
			}
		}
		if checked == 0 {
			t.Errorf("%s: parsed 0 containers — this guard has stopped guarding", path)
		}
	}
}

// decodeAll parses every document in a multi-document manifest.
func decodeAll(t *testing.T, path, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	dec := yaml.NewDecoder(strings.NewReader(body))
	for {
		var d map[string]any
		err := dec.Decode(&d)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if d != nil {
			out = append(out, d)
		}
	}
}

// containersOf returns a Deployment's containers, or nothing for other kinds.
func containersOf(doc map[string]any) []map[string]any {
	spec, _ := doc["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	pod, _ := tmpl["spec"].(map[string]any)
	raw, _ := pod["containers"].([]any)
	var out []map[string]any
	for _, c := range raw {
		if m, ok := c.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// A PVC with no storageClassName relies on the cluster having a default one.
// Plenty do not, including the cluster this was tested on, and the failure is
// silent and misleading: PVC Pending, pod Pending, no node, no container
// events, and nothing naming storage as the cause.
func TestPVCNamesAStorageClass(t *testing.T) {
	for path, body := range manifests(t) {
		if !strings.Contains(body, "kind: PersistentVolumeClaim") {
			continue
		}
		if !strings.Contains(body, "storageClassName:") {
			t.Errorf("%s: PVC without storageClassName — it will sit Pending forever "+
				"on any cluster with no default StorageClass, with no error naming storage", path)
		}
	}
}

// ssh refuses a private key that any group or other can read. A Secret volume
// cannot be made private enough on its own: defaultMode: 0400 is silently
// widened to 0440 by the pod's own fsGroup, which the kubelet applies to every
// mounted volume. The two settings cancel out and the tunnel never comes up.
//
// The fix is to copy the key somewhere private before use, so that is what is
// asserted — not the mode, which is the thing that does not work.
func TestSSHKeyIsCopiedToAPrivatePathBeforeUse(t *testing.T) {
	body := manifests(t)["k8s/merino-ssh.yaml"]
	if body == "" {
		t.Fatal("k8s/merino-ssh.yaml missing — this guard has stopped guarding")
	}
	if !strings.Contains(body, "install -m 0600") {
		t.Error("the ssh key must be copied to a 0600 path before use: a Secret " +
			"volume's defaultMode is widened to 0440 by fsGroup, and ssh then " +
			"refuses the key with \"bad permissions\"")
	}
	if strings.Contains(body, "-i /etc/merino/ssh/id_ed25519") {
		t.Error("ssh is still reading the key straight from the Secret mount, " +
			"which fsGroup has made group-readable")
	}
}

// OpenSSH wraps its bind in umask(0177), so a forwarded unix socket is always
// 0600 owned by the ssh process's uid. merinod runs as a different uid, so
// without re-moding the socket it gets "connect: permission denied" forever
// while the pod reports 2/2 Ready — /healthz reports the process, not the herd.
func TestForwardedSocketIsMadeReachableByMerinod(t *testing.T) {
	body := manifests(t)["k8s/merino-ssh.yaml"]
	if body == "" {
		t.Fatal("k8s/merino-ssh.yaml missing — this guard has stopped guarding")
	}
	if !strings.Contains(body, "chmod 0660 /run/herdr/herdr.sock") {
		t.Error("the forwarded socket is created 0600 by ssh and owned by the " +
			"sidecar's uid; without re-moding it, merinod cannot open it")
	}
}
