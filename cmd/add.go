package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/output"
	"github.com/BU-Neuromics/datapin/internal/pathutil"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

var addCmd = &cobra.Command{
	Use:   "add <local-path> [<project>:]<remote-path>",
	Short: "Track a local file or directory in the gosf manifest",
	Long: `Add a local file or directory to gosf.toml so gosf keeps it in sync with OSF.
Use 'gosf pull' to record files that already exist on the remote.

If <remote-path> is omitted the remote path mirrors the local path.
If <local-path> is a directory, all files in it are added recursively.

Path rules follow scp conventions:
  gosf add data/file.txt                       remote: /data/file.txt (mirror)
  gosf add data/file.txt abc12:/results/       remote: /results/file.txt
  gosf add data/file.txt abc12:/results/out.txt  remote: /results/out.txt
  gosf add data/dir/ abc12:/results/           remote: /results/<files> (contents)
  gosf add data/dir  abc12:/results/           remote: /results/dir/<files> (dir itself)`,
	Args:         cobra.RangeArgs(1, 2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		srcArg := args[0]
		var destArg string
		if len(args) == 2 {
			destArg = args[1]
		}

		srcTrailingSlash := strings.HasSuffix(srcArg, "/") || strings.HasSuffix(srcArg, string(filepath.Separator))
		cutset := "/"
		if string(filepath.Separator) != "/" {
			cutset += string(filepath.Separator)
		}
		localSrc := strings.TrimRight(srcArg, cutset)

		srcInfo, statErr := os.Stat(localSrc)
		srcIsDir := statErr == nil && srcInfo.IsDir()

		// Parse remote dest for project and path.
		var nodeID, remoteDest string
		if destArg != "" {
			if strings.Contains(destArg, ":") {
				target, err := resolver.ParseTarget(destArg)
				if err != nil {
					return err
				}
				nodeID = target.NodeID
				remoteDest = target.Path
				// Normalise: ParseTarget always returns path starting with /;
				// if it ends with / (root or explicit dir) keep the slash for
				// pathutil to honour.
				if strings.HasSuffix(destArg[strings.Index(destArg, ":")+1:], "/") &&
					!strings.HasSuffix(remoteDest, "/") {
					remoteDest += "/"
				}
			} else {
				remoteDest = destArg
				if !strings.HasPrefix(remoteDest, "/") {
					remoteDest = "/" + remoteDest
				}
			}
		}

		// Load or create gosf.toml.
		manifestPath, _, findErr := manifest.FindManifest()
		var m *manifest.Manifest
		manifestCreated := false
		if manifest.IsNotFound(findErr) {
			if nodeID == "" {
				return fmt.Errorf("no project configured — run: gosf init <project-id>")
			}
			manifestPath = filepath.Join(".gosf", "gosf.toml")
			m = &manifest.Manifest{Project: manifest.ProjectConfig{ID: nodeID}}
			manifestCreated = true
		} else if findErr != nil {
			return findErr
		} else {
			var err error
			m, err = manifest.Load(manifestPath)
			if err != nil {
				return err
			}
		}

		// Resolve project.
		if nodeID == "" {
			nodeID = m.Project.ID
		}
		if nodeID == "" {
			return fmt.Errorf("no project configured — run: gosf init <project-id>")
		}

		// Determine per-entry project field (empty when nodeID matches manifest default).
		entryProject := ""
		if nodeID != m.Project.ID {
			entryProject = nodeID
		}

		token := config.LoadToken(flagToken)
		c := client.New(token)
		res := resolver.New(c)

		var entries []manifest.Entry
		var addEntries []output.AddEntry

		if srcIsDir {
			// Directory: recurse and create one entry per file.
			localBase, remoteBase := pathutil.PushDirBases(localSrc, remoteDest, srcTrailingSlash)
			err := filepath.WalkDir(localSrc, func(path string, d os.DirEntry, walkErr error) error {
				if walkErr != nil || d.IsDir() {
					return walkErr
				}
				remotePath := pathutil.MapFilePath(localBase, remoteBase, filepath.ToSlash(path))
				if findEntryByLocal(m, path) >= 0 {
					return fmt.Errorf("entry with local path %q already exists in .gosf/gosf.toml", path)
				}
				e := manifest.Entry{
					Local:   path,
					Remote:  remotePath,
					Project: entryProject,
				}
				entries = append(entries, e)
				addEntries = append(addEntries, output.AddEntry{
					Local:   path,
					Remote:  remotePath,
					Project: nodeID,
				})
				return nil
			})
			if err != nil {
				return err
			}
		} else {
			// Single file.
			remotePath := pathutil.FileRemotePath(localSrc, remoteDest)
			if findEntryByLocal(m, localSrc) >= 0 {
				return fmt.Errorf("entry with local path %q already exists in .gosf/gosf.toml", localSrc)
			}

			entry := manifest.Entry{
				Local:   localSrc,
				Remote:  remotePath,
				Project: entryProject,
			}

			// Fetch remote version if the file already exists on OSF.
			if existingItem, err := res.Resolve(cmd.Context(), nodeID, remotePath); err == nil {
				if versions, err := c.GetFileVersions(cmd.Context(), existingItem.ID); err == nil && len(versions) > 0 {
					latest := versions[0]
					entry.Version = latest.Number()
					entry.MD5 = latest.Attributes.Extra.Hashes.MD5
				}
			}

			entries = []manifest.Entry{entry}
			addEntries = []output.AddEntry{{
				Local:   localSrc,
				Remote:  remotePath,
				Project: nodeID,
				Version: entry.Version,
				MD5:     entry.MD5,
			}}
		}

		if len(entries) == 0 {
			return fmt.Errorf("no files found under %q", localSrc)
		}

		m.Files = append(m.Files, entries...)

		if err := manifest.Save(m, manifestPath); err != nil {
			return err
		}

		jsonMode := flagOutput == "json"
		if jsonMode {
			return output.PrintJSON(os.Stdout, output.AddResult{
				Entries:         addEntries,
				ManifestCreated: manifestCreated,
			})
		}

		for _, e := range addEntries {
			if e.Version > 0 {
				log.Infof("added %s → %s:%s  (v%d)", e.Local, e.Project, e.Remote, e.Version)
			} else {
				log.Infof("added %s → %s:%s  (not yet pushed)", e.Local, e.Project, e.Remote)
			}
		}

		// Warn about large local files.
		for _, e := range entries {
			if info, err := os.Stat(e.Local); err == nil && info.Size() > 50*1024*1024 {
				log.Warnf("consider adding %s to .gitignore (%s)", e.Local, formatSizeMB(info.Size()))
			}
		}

		return nil
	},
}

// findEntryByLocal returns the index of the first manifest entry with the
// given local path, or -1 if not found.
func findEntryByLocal(m *manifest.Manifest, local string) int {
	for i, e := range m.Files {
		if e.Local == local {
			return i
		}
	}
	return -1
}

// findEntryByRemote returns the index of the first manifest entry whose
// resolved project and remote path match, or -1 if not found.
func findEntryByRemote(m *manifest.Manifest, projectID, remotePath string) int {
	for i, e := range m.Files {
		if e.Remote == remotePath && e.ResolveProject(m.Project.ID) == projectID {
			return i
		}
	}
	return -1
}

func formatSizeMB(n int64) string {
	return fmt.Sprintf("%.0f MB", float64(n)/1024/1024)
}

func init() {
	rootCmd.AddCommand(addCmd)
}
