package daemon

import (
	"crypto/sha256"
	"fmt"
	"sort"
)

// ResourceRef identifies a logical user resource independently of its transport.
type ResourceRef struct {
	Server string `json:"server,omitempty"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

func (r ResourceRef) Label() string {
	label := r.Name + "/" + r.Kind
	if r.Kind == "scratch" {
		label = "scratch/" + r.Name
	}
	if r.Kind == "temporary" {
		label = r.Name
	}
	if r.Detail != "" {
		label += "/" + r.Detail
	}
	if r.Server != "" {
		label = r.Server + "/" + label
	}
	return label
}

func uniqueResources(refs []ResourceRef) []ResourceRef {
	seen := map[ResourceRef]bool{}
	out := []ResourceRef{}
	for _, r := range refs {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label() < out[j].Label() })
	return out
}

// ConnectionID is shared by inventory and resource views. A replacement master
// has a new socket or PID and therefore a new display identity.
func ConnectionID(c ConnectionView, daemonPID int, runtimeDir string) string {
	prefix, identity, pid := "ssh-mux", c.Socket, c.PID
	if c.Kind == "sshfs" {
		prefix, identity, pid = "sshfs", c.ID, c.HelperPID
	}
	if c.Kind == "local-forward" || c.Kind == "local-web" {
		prefix, identity, pid = "daemon", runtimeDir, daemonPID
	}
	if identity == "" {
		identity = c.ID
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%d", prefix, identity, pid)))
	return fmt.Sprintf("%s-%x", prefix, hash[:5])
}

func (f *managedForward) resourceRefs() []ResourceRef {
	refs := []ResourceRef{}
	for owner, resources := range f.resources {
		if owner == "" || f.owners[owner] {
			refs = append(refs, resources...)
		}
	}
	return uniqueResources(refs)
}
