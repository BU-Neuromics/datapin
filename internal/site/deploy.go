package site

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WriteDir materializes a rendered site under dir.
func WriteDir(s Site, dir string) error {
	for path, content := range s.Files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(full, content, 0644); err != nil {
			return err
		}
	}
	return nil
}

// DeployGHPages publishes dir as a single orphan commit force-pushed to
// the gh-pages branch of remoteURL (plan §2.6: no history accumulation,
// no workflow file). It shells out to system git, inheriting the user's
// credential helpers; token (optional) is injected for https GitHub
// remotes via an ephemeral basic-auth header, never written to disk.
func DeployGHPages(ctx context.Context, dir, remoteURL, token string) error {
	work, err := os.MkdirTemp("", "datapin-site-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()

	// Copy the built site into a fresh repo (never git-init the build dir:
	// it may live inside the project repo).
	if err := copyTree(dir, work); err != nil {
		return err
	}

	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=datapin", "GIT_AUTHOR_EMAIL=datapin@localhost",
			"GIT_COMMITTER_NAME=datapin", "GIT_COMMITTER_EMAIL=datapin@localhost",
		)
		var errBuf bytes.Buffer
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), sanitize(errBuf.String(), token), err)
		}
		return nil
	}

	if err := run("init", "-q", "-b", "gh-pages"); err != nil {
		return err
	}
	if err := run("add", "-A"); err != nil {
		return err
	}
	if err := run("commit", "-q", "-m", "site: publish"); err != nil {
		return err
	}
	pushArgs := []string{"push", "-q", "--force", remoteURL, "gh-pages:gh-pages"}
	if token != "" && strings.HasPrefix(remoteURL, "https://") {
		header := "AUTHORIZATION: basic " + basicAuth("x-access-token", token)
		pushArgs = append([]string{"-c", "http." + remoteURL + ".extraheader=" + header}, pushArgs...)
	}
	return run(pushArgs...)
}

// EnablePages turns on GitHub Pages for repo ("owner/name"), serving the
// gh-pages branch, via the REST API (first deploy only — the user never
// opens the settings UI). A 409 means Pages is already configured.
func EnablePages(ctx context.Context, apiBase, repo, token string) error {
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	body := strings.NewReader(`{"build_type":"legacy","source":{"branch":"gh-pages","path":"/"}}`)
	req, err := http.NewRequestWithContext(ctx, "POST",
		strings.TrimSuffix(apiBase, "/")+"/repos/"+repo+"/pages", body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusCreated:
		return nil
	case http.StatusConflict: // already enabled
		return nil
	default:
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("enabling GitHub Pages for %s: HTTP %d: %s", repo, resp.StatusCode, bytes.TrimSpace(data))
	}
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	})
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// sanitize scrubs the token from git error output before it reaches logs.
func sanitize(s, token string) string {
	if token == "" {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(strings.ReplaceAll(s, token, "REDACTED"))
}
