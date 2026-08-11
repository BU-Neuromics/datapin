package cmd

import (
	"sort"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

// datasetAction is what the publish transaction does to one key of the
// new version draft. The draft starts as a files-import copy of the
// previous version, so "keep" costs nothing and only changed content
// moves bytes (plan §4.1).
type datasetAction string

const (
	actionUpload  datasetAction = "upload"  // new key: upload
	actionReplace datasetAction = "replace" // changed content: delete imported copy, upload
	actionKeep    datasetAction = "keep"    // unchanged: rely on files-import
	actionRemove  datasetAction = "remove"  // key left the manifest: delete from draft
)

// datasetPlanEntry is one key's fate in the transaction.
type datasetPlanEntry struct {
	Key      string
	Local    string // local path; "" for actionRemove
	Action   datasetAction
	LocalMD5 string // md5 the key will have after publish (local content)
}

// planDataset compares the manifest's file list (with computed local MD5s)
// against the remote latest version's file set and returns the per-key
// actions, sorted by key. remote is key → md5 of the latest published
// version (nil for a first publish). Pure — the transaction executes it.
func planDataset(files []manifest.DatasetFile, localMD5 map[string]string, remote map[string]string) []datasetPlanEntry {
	plan := make([]datasetPlanEntry, 0, len(files))
	inManifest := make(map[string]bool, len(files))
	for _, f := range files {
		inManifest[f.Key] = true
		local := localMD5[f.Local]
		remoteMD5, onRemote := remote[f.Key]
		var action datasetAction
		switch {
		case !onRemote:
			action = actionUpload
		case local == remoteMD5:
			action = actionKeep
		default:
			action = actionReplace
		}
		plan = append(plan, datasetPlanEntry{Key: f.Key, Local: f.Local, Action: action, LocalMD5: local})
	}
	for key := range remote {
		if !inManifest[key] {
			plan = append(plan, datasetPlanEntry{Key: key, Action: actionRemove})
		}
	}
	sort.Slice(plan, func(i, j int) bool { return plan[i].Key < plan[j].Key })
	return plan
}

// planHasWork reports whether the plan changes anything — a plan of pure
// keeps means the new version would be byte-identical to the previous one.
func planHasWork(plan []datasetPlanEntry) bool {
	for _, e := range plan {
		if e.Action != actionKeep {
			return true
		}
	}
	return false
}

// datasetState is the dataset-level analogue of ClassifyFile, aggregated
// over the whole file set (plan §4.1). localChanged: any file differs from
// its pin; remoteNewer: a version newer than the pin is published;
// localMissing: any tracked file is absent locally.
func datasetState(published, localChanged, remoteNewer, localMissing bool) string {
	switch {
	case !published:
		return "NOT_PUBLISHED"
	case localMissing:
		return "MISSING"
	case localChanged && remoteNewer:
		return "DIVERGED"
	case localChanged:
		return "AHEAD"
	case remoteNewer:
		return "REMOTE_NEWER"
	default:
		return "IN_SYNC"
	}
}
