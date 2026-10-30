// Package gitrepo keeps the catalog in a Git repository (ADR-0029): it
// reads the item files on a branch, and commits and pushes rvav's changes
// on top of what it read, through go-git (no git binary). HTTPS with a
// token or SSH with a deploy key; local paths only for tests
// (UseInProcessFiles).
package gitrepo

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5/util"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/go-git/go-git/v5/storage/memory"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Options: where the repository is, and how rvav signs in.
type Options struct {
	URL    string // https://... or ssh://... (git@host:path too)
	Branch string // main when empty
	Dir    string // the directory of item files, catalog when empty
	// Username is HTTPS's (rvav when empty) or SSH's (git when empty).
	Username string
	// Credential is a token or password (HTTPS), or a private key in PEM
	// (SSH).
	Credential string
	// KnownHosts are the SSH server's host keys, as known_hosts lines.
	KnownHosts string
	// Insecure skips the TLS check (HTTPS) or accepts any host key (SSH).
	Insecure bool
	// Committer is rvav's identity on the commits it makes (the author is
	// the administrator who published).
	Committer Person
	// Cache is where the clone is kept between reads.
	Cache string
}

// Person is a commit's author or committer.
type Person struct{ Name, Email string }

// File is an item file on the branch.
type File struct {
	Path    string // in the repository
	Slug    string // its name, without .yaml
	Hash    string // its blob's
	Content []byte
	// TooLarge: over MaxFileSize, its content not read.
	TooLarge bool
}

// MaxFileSize is the largest item file read, as the YAML import takes.
const MaxFileSize = 2 << 20

// Commit is a commit on the branch.
type Commit struct {
	SHA     string    `json:"sha"`
	Message string    `json:"message"`
	Author  string    `json:"author"`
	Email   string    `json:"email,omitempty"`
	When    time.Time `json:"when"`
}

// Snapshot is the branch as read: its head (zero for an empty
// repository) and its item files by slug.
type Snapshot struct {
	Head  Commit
	Files map[string]File
}

// Change is a file to write, or to delete (Content nil).
type Change struct {
	Path    string
	Content []byte
}

// BlobHash is the hash Git gives a file of that content.
func BlobHash(content []byte) string {
	return plumbing.ComputeHash(plumbing.BlobObject, content).String()
}

// ErrRejected: the branch moved since it was read; read it again.
var ErrRejected = errors.New("the branch moved since rvav read it")

var inProcess struct {
	sync.Mutex
	on bool
}

// UseInProcessFiles serves local repositories (file://) in process, for
// tests: rvav's image has no git binary, and production refuses local
// paths.
func UseInProcessFiles() {
	inProcess.Lock()
	defer inProcess.Unlock()
	if !inProcess.on {
		client.InstallProtocol("file", server.NewClient(server.DefaultLoader))
		inProcess.on = true
	}
}

func localAllowed() bool {
	inProcess.Lock()
	defer inProcess.Unlock()
	return inProcess.on
}

// Repo is a repository rvav keeps its catalog in.
type Repo struct {
	o    Options
	auth transport.AuthMethod
	mu   sync.Mutex // one read or commit at a time in this process
}

// New checks the options and prepares how to sign in.
func New(o Options) (*Repo, error) {
	// A token or key pasted with a line break after it.
	o.Credential = strings.TrimSpace(o.Credential)
	o.Branch, o.Dir = cmp.Or(o.Branch, "main"), strings.Trim(cmp.Or(o.Dir, "catalog"), "/")
	o.Committer.Name, o.Committer.Email = cmp.Or(o.Committer.Name, "rvav"), cmp.Or(o.Committer.Email, "rvav@localhost")
	if o.URL == "" {
		return nil, errors.New("give the repository's URL")
	}
	if strings.Contains(o.Dir, "..") {
		return nil, fmt.Errorf("the directory %q leaves the repository", o.Dir)
	}
	if o.Cache != "" {
		// A clone per repository and branch: nothing of one read as another's.
		sum := sha256.Sum256([]byte(o.URL + "\x00" + o.Branch))
		o.Cache = filepath.Join(o.Cache, hex.EncodeToString(sum[:8]))
	}
	if err := CheckURL(o.URL); err != nil {
		return nil, err
	}
	r := &Repo{o: o}
	switch scheme := urlScheme(o.URL); scheme {
	case "https", "http":
		if o.Credential != "" {
			r.auth = &githttp.BasicAuth{Username: cmp.Or(o.Username, "rvav"), Password: o.Credential}
		}
	case "ssh":
		if o.Credential == "" {
			return nil, errors.New("give the deploy key: its private key, in PEM")
		}
		keys, err := gitssh.NewPublicKeys(cmp.Or(o.Username, "git"), []byte(o.Credential), "")
		if err != nil {
			return nil, fmt.Errorf("the deploy key: %w", err)
		}
		switch {
		case o.Insecure:
			keys.HostKeyCallback = gossh.InsecureIgnoreHostKey()
		case strings.TrimSpace(o.KnownHosts) == "":
			return nil, errors.New("give the server's host keys (known_hosts lines), or don't check them (insecure)")
		default:
			cb, err := hostKeys(o.KnownHosts)
			if err != nil {
				return nil, err
			}
			keys.HostKeyCallback = cb
		}
		r.auth = keys
	}
	return r, nil
}

// CheckURL says whether rvav can use a repository's URL: https://,
// ssh:// or git@host:path (and file:// in tests).
func CheckURL(u string) error {
	if p, err := url.Parse(u); err == nil && p.User != nil {
		if _, ok := p.User.Password(); ok {
			// It would be shown with the URL, to catalog admins too.
			return errors.New("the URL holds a password or token: give it as the token, not in the URL")
		}
	}
	switch urlScheme(u) {
	case "https", "http", "ssh":
		return nil
	case "file":
		if localAllowed() {
			return nil
		}
		return errors.New("a local path is not a repository rvav can use: give an https:// or ssh:// URL")
	}
	return errors.New("an https:// or ssh:// URL, or git@host:path")
}

// Scheme is how rvav reaches a repository: HTTPS or SSH.
func Scheme(u string) string { return strings.ToUpper(urlScheme(u)) }

// urlScheme is a URL's scheme; git@host:path is SSH's.
func urlScheme(u string) string {
	if p, err := url.Parse(u); err == nil && p.Scheme != "" && strings.Contains(u, "://") {
		return strings.ToLower(p.Scheme)
	}
	if at, colon := strings.Index(u, "@"), strings.Index(u, ":"); at > 0 && colon > at {
		return "ssh"
	}
	return ""
}

// hostKeys checks SSH host keys against known_hosts lines.
func hostKeys(lines string) (gossh.HostKeyCallback, error) {
	f, err := os.CreateTemp("", "rvav-known-hosts-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(lines + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()
	cb, err := knownhosts.New(f.Name())
	if err != nil {
		return nil, fmt.Errorf("the host keys: %w", err)
	}
	return cb, nil
}

func (r *Repo) insecure() bool { return r.o.Insecure && urlScheme(r.o.URL) != "ssh" }

// Branch and Dir are the options as used.
func (r *Repo) Branch() string { return r.o.Branch }
func (r *Repo) Dir() string    { return r.o.Dir }

// FilePath is an item's file in the repository.
func (r *Repo) FilePath(slug string) string { return path.Join(r.o.Dir, slug+".yaml") }

// Check signs in and reads the branch's head without cloning: zero for an
// empty repository; an error when the branch is missing from one that
// isn't.
func (r *Repo) Check(ctx context.Context) (string, error) {
	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: "origin", URLs: []string{r.o.URL}})
	refs, err := remote.ListContext(ctx, &git.ListOptions{Auth: r.auth, InsecureSkipTLS: r.insecure()})
	if errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var branches []string
	for _, ref := range refs {
		if ref.Name() == plumbing.NewBranchReferenceName(r.o.Branch) {
			return ref.Hash().String(), nil
		}
		if ref.Name().IsBranch() {
			branches = append(branches, ref.Name().Short())
		}
	}
	if len(branches) == 0 {
		return "", nil
	}
	sort.Strings(branches)
	return "", fmt.Errorf("the repository has no branch %s (it has %s)", r.o.Branch, strings.Join(branches, ", "))
}

func (r *Repo) remoteRef() plumbing.ReferenceName {
	return plumbing.NewRemoteReferenceName("origin", r.o.Branch)
}

// open opens the clone, or makes it, and fetches the branch.
func (r *Repo) open(ctx context.Context) (*git.Repository, error) {
	if r.o.Cache == "" {
		return nil, errors.New("no directory to keep the clone in")
	}
	repo, err := git.PlainOpen(r.o.Cache)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		if err := os.MkdirAll(r.o.Cache, 0o700); err != nil {
			return nil, err
		}
		repo, err = git.PlainInit(r.o.Cache, false)
	}
	if err != nil {
		return nil, err
	}
	// The remote as configured now (its URL may have changed).
	if rem, err := repo.Remote("origin"); err != nil || rem.Config().URLs[0] != r.o.URL {
		_ = repo.DeleteRemote("origin")
		if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{r.o.URL}}); err != nil {
			return nil, err
		}
	}
	spec := config.RefSpec("+" + plumbing.NewBranchReferenceName(r.o.Branch).String() + ":" + r.remoteRef().String())
	err = repo.FetchContext(ctx, &git.FetchOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{spec}, Auth: r.auth,
		InsecureSkipTLS: r.insecure(), Force: true, Tags: git.NoTags})
	switch {
	case err == nil, errors.Is(err, git.NoErrAlreadyUpToDate):
	case errors.Is(err, transport.ErrEmptyRemoteRepository), strings.Contains(err.Error(), "couldn't find remote ref"):
		// No branch yet (an empty repository too): rvav's first commit
		// makes it. Nothing of what was fetched before is its.
		_ = repo.Storer.RemoveReference(r.remoteRef())
	default:
		return nil, err
	}
	return repo, nil
}

// head is the fetched branch's commit, nil when it has none.
func (r *Repo) head(repo *git.Repository) (*object.Commit, error) {
	ref, err := repo.Reference(r.remoteRef(), true)
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return repo.CommitObject(ref.Hash())
}

func commitOf(c *object.Commit) Commit {
	if c == nil {
		return Commit{}
	}
	return Commit{SHA: c.Hash.String(), Message: strings.TrimSpace(c.Message), Author: c.Author.Name, Email: c.Author.Email,
		When: c.Author.When.UTC()}
}

// Read fetches the branch and reads its item files.
func (r *Repo) Read(ctx context.Context) (Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	repo, err := r.open(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return r.snapshot(repo)
}

func (r *Repo) snapshot(repo *git.Repository) (Snapshot, error) {
	out := Snapshot{Files: map[string]File{}}
	c, err := r.head(repo)
	if err != nil || c == nil {
		return out, err
	}
	out.Head = commitOf(c)
	tree, err := c.Tree()
	if err != nil {
		return out, err
	}
	dir := tree
	if r.o.Dir != "" && r.o.Dir != "." {
		if dir, err = tree.Tree(r.o.Dir); errors.Is(err, object.ErrDirectoryNotFound) {
			return out, nil
		} else if err != nil {
			return out, err
		}
	}
	for _, e := range dir.Entries {
		slug, ok := strings.CutSuffix(e.Name, ".yaml")
		if !ok || !e.Mode.IsFile() {
			continue
		}
		f, err := dir.TreeEntryFile(&e)
		if err != nil {
			return out, err
		}
		file := File{Path: path.Join(r.o.Dir, e.Name), Slug: slug, Hash: e.Hash.String()}
		if f.Size > MaxFileSize {
			// Not read: refused as it is.
			file.TooLarge = true
		} else {
			content, err := f.Contents()
			if err != nil {
				return out, err
			}
			file.Content = []byte(content)
		}
		out.Files[slug] = file
	}
	return out, nil
}

// LastCommit is the newest commit on the branch that changed a file.
func (r *Repo) LastCommit(ctx context.Context, file string) (Commit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	repo, err := git.PlainOpen(r.o.Cache)
	if err != nil {
		return Commit{}, err
	}
	c, err := r.head(repo)
	if err != nil || c == nil {
		return Commit{}, err
	}
	iter, err := repo.Log(&git.LogOptions{From: c.Hash, FileName: &file})
	if err != nil {
		return Commit{}, err
	}
	defer iter.Close()
	found, err := iter.Next()
	if err != nil {
		return Commit{}, nil // not on the branch
	}
	return commitOf(found), nil
}

// Commit makes one commit of the changes on the branch as of base (the
// head a Read returned; "" for an empty branch) and pushes it. ErrRejected
// when the branch has moved since. Changes that change nothing make no
// commit: the head is returned.
func (r *Repo) Commit(ctx context.Context, base string, changes []Change, author Person, message string) (Commit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	repo, err := r.open(ctx)
	if err != nil {
		return Commit{}, err
	}
	head, err := r.head(repo)
	if err != nil {
		return Commit{}, err
	}
	if commitOf(head).SHA != base {
		return Commit{}, ErrRejected
	}
	branch := plumbing.NewBranchReferenceName(r.o.Branch)
	if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, branch)); err != nil {
		return Commit{}, err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return Commit{}, err
	}
	if head != nil {
		if err := repo.Storer.SetReference(plumbing.NewHashReference(branch, head.Hash)); err != nil {
			return Commit{}, err
		}
		if err := wt.Reset(&git.ResetOptions{Commit: head.Hash, Mode: git.HardReset}); err != nil {
			return Commit{}, err
		}
	} else {
		// An empty branch: nothing of an earlier clone left in the tree,
		// nor in the index.
		_ = repo.Storer.RemoveReference(branch)
		if err := clearTree(r.o.Cache); err != nil {
			return Commit{}, err
		}
		if err := repo.Storer.SetIndex(&index.Index{Version: 2}); err != nil {
			return Commit{}, err
		}
	}
	for _, ch := range changes {
		if strings.Contains(ch.Path, "..") || path.IsAbs(ch.Path) {
			return Commit{}, fmt.Errorf("%q leaves the repository", ch.Path)
		}
		if ch.Content == nil {
			if _, err := wt.Remove(ch.Path); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, object.ErrFileNotFound) {
				return Commit{}, err
			}
			continue
		}
		if err := util.WriteFile(wt.Filesystem, ch.Path, ch.Content, 0o644); err != nil {
			return Commit{}, err
		}
		if _, err := wt.Add(ch.Path); err != nil {
			return Commit{}, err
		}
	}
	now := time.Now()
	hash, err := wt.Commit(message, &git.CommitOptions{
		Author:    &object.Signature{Name: cmp.Or(author.Name, r.o.Committer.Name), Email: cmp.Or(author.Email, r.o.Committer.Email), When: now},
		Committer: &object.Signature{Name: r.o.Committer.Name, Email: r.o.Committer.Email, When: now},
	})
	if errors.Is(err, git.ErrEmptyCommit) {
		return commitOf(head), nil
	}
	if err != nil {
		return Commit{}, err
	}
	spec := config.RefSpec(branch.String() + ":" + branch.String())
	err = repo.PushContext(ctx, &git.PushOptions{RemoteName: "origin", RefSpecs: []config.RefSpec{spec}, Auth: r.auth,
		InsecureSkipTLS: r.insecure()})
	switch {
	case err == nil, errors.Is(err, git.NoErrAlreadyUpToDate):
	case errors.Is(err, git.ErrNonFastForwardUpdate), strings.Contains(err.Error(), "non-fast-forward"),
		strings.Contains(err.Error(), "fetch first"):
		return Commit{}, ErrRejected
	default:
		return Commit{}, err
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(r.remoteRef(), hash)); err != nil {
		return Commit{}, err
	}
	c, err := repo.CommitObject(hash)
	if err != nil {
		return Commit{}, err
	}
	return commitOf(c), nil
}

// clearTree removes the work tree's files, not .git.
func clearTree(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != ".git" {
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
