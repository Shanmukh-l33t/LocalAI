//go:build linux

// SPDX-License-Identifier: MIT
package addons

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/uploads"
	"github.com/mudler/LocalAI/pkg/addons/zipformat"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("published add-on packages", func() {
	It("keeps the current and the previous generation of every add-on", func() {
		r := newUpdateRig(true)
		r.m.SetUploads(&uploads.Store{Root: filepath.Join(r.cfg.Root, "uploads")})
		packagesRoot := filepath.Join(r.cfg.Root, "packages")

		// installOther adds a second add-on of a single generation from a local
		// file and returns the package it published.
		installOther := func() string {
			raw, e := os.ReadFile("../../../tests/addons/fixtures/packages/manifest.json")
			Expect(e).NotTo(HaveOccurred())
			var m map[string]any
			Expect(json.Unmarshal(raw, &m)).To(Succeed())
			m["id"], m["version"], m["executable"] = "other", "1.0.0", "bin/other"
			mb, e := json.Marshal(m)
			Expect(e).NotTo(HaveOccurred())
			var b bytes.Buffer
			Expect(zipformat.Write(&b, "other", "1.0.0", "linux", "amd64", "bin/other", []zipformat.Entry{
				{Name: "manifest.json", Data: mb}, {Name: "bin/other", Data: []byte("other"), Exec: true},
			})).To(Succeed())
			id, _, e := r.m.FileBegin(ctx, actor, "other.zip")
			Expect(e).NotTo(HaveOccurred())
			data := b.Bytes()
			for off := 0; off < len(data); off += 1000 {
				Expect(r.m.FileChunk(ctx, actor, id, int64(off), data[off:min(off+1000, len(data))])).To(Succeed())
			}
			v, e := r.m.FileFinish(ctx, actor, id)
			Expect(e).NotTo(HaveOccurred())
			_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "other", AddonID: "other", Action: "install",
				SourceID: zipformat.SourceID, Version: v.FileID, Approval: &HostApproval{Digest: "sha256:" + v.SHA256}})
			Expect(e).NotTo(HaveOccurred())
			st, e := r.m.Status(ctx, "other")
			Expect(e).NotTo(HaveOccurred())
			return st.Generations[0].Receipt.Directory
		}
		other := installOther()
		// A directory the installer did not publish is never touched.
		Expect(os.MkdirAll(filepath.Join(packagesRoot, "keep-me"), 0700)).To(Succeed())

		// Three generations of the first add-on, each publishing its own package.
		installed := r.installChannel()
		digest := installed.Generations[0].Receipt.Digest
		for i := range 2 {
			rel := r.lib.publish("1.2.3-dev", "development", fmt.Sprintf("build-%d", i+2), catalog.Requirements{})
			r.lib.refresh()
			_, e := r.m.Submit(ctx, actor, upgrade(fmt.Sprintf("u%d", i+1), digest, rel.Digest))
			Expect(e).NotTo(HaveOccurred())
			digest = rel.Digest
		}
		st, e := r.m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Generations).To(HaveLen(3))
		oldest := st.Generations[0].Receipt.Directory
		previous := st.Generations[1].Receipt.Directory
		current := st.Generations[2].Receipt.Directory

		// The two add-ons live in one packages root: after three commits the
		// first keeps its current and previous package and the second its only
		// one, whatever is oldest.
		entries, e := os.ReadDir(packagesRoot)
		Expect(e).NotTo(HaveOccurred())
		published := 0
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "package-stage-") {
				published++
			}
		}
		_, e = os.Lstat(oldest)
		Expect(os.IsNotExist(e)).To(BeTrue(), "the package two generations back is still on disk; %d package directories remain", published)
		Expect(published).To(Equal(3), "the packages of the current and the previous generation")
		for _, dir := range []string{previous, current, other} {
			info, e := os.Lstat(dir)
			Expect(e).NotTo(HaveOccurred(), dir)
			Expect(info.IsDir()).To(BeTrue(), dir)
		}
		info, e := os.Lstat(filepath.Join(packagesRoot, "keep-me"))
		Expect(e).NotTo(HaveOccurred())
		Expect(info.IsDir()).To(BeTrue())

		// A host that ran the old code holds packages no generation names; the
		// startup reconcile removes them without waiting for a commit.
		orphan := filepath.Join(packagesRoot, "package-stage-orphan")
		Expect(os.MkdirAll(orphan, 0700)).To(Succeed())
		Expect(r.m.Reconcile(ctx)).To(Succeed())
		Expect(orphan).NotTo(BeADirectory())
		Expect(current).To(BeADirectory())
	})
})
