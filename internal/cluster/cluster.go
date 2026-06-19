// Package cluster detects suspicious timestamp clusters: a large number of
// files within one source sharing a single identical mtime, which is a likely
// sign the backup process clobbered modification times to the copy date.
package cluster

import (
	"sort"

	"github.com/janulbrich/backup-crunch/internal/model"
)

// MaxSamplePaths bounds how many example paths a cluster records.
const MaxSamplePaths = 10

type key struct {
	src int
	mt  int64 // mtime as UnixNano
}

// Detect returns, per source, every (source, mtime) group whose file count is
// at least threshold. Results are deterministically ordered.
func Detect(files []model.File, threshold int) []model.Cluster {
	groups := make(map[key][]model.File)
	roots := make(map[int]string)
	for _, f := range files {
		k := key{f.SourceIndex, f.ModTime.UnixNano()}
		groups[k] = append(groups[k], f)
		roots[f.SourceIndex] = f.SourceRoot
	}

	var clusters []model.Cluster
	for k, fs := range groups {
		if len(fs) < threshold {
			continue
		}
		sort.Slice(fs, func(i, j int) bool { return fs[i].RelPath < fs[j].RelPath })
		var samples []string
		for i := 0; i < len(fs) && i < MaxSamplePaths; i++ {
			samples = append(samples, fs[i].RelPath)
		}
		clusters = append(clusters, model.Cluster{
			SourceIndex: k.src,
			SourceRoot:  roots[k.src],
			ModTime:     fs[0].ModTime,
			FileCount:   len(fs),
			SamplePaths: samples,
		})
	}

	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].SourceIndex != clusters[j].SourceIndex {
			return clusters[i].SourceIndex < clusters[j].SourceIndex
		}
		return clusters[i].ModTime.Before(clusters[j].ModTime)
	})
	return clusters
}
