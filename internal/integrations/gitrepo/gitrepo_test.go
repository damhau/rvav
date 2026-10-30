package gitrepo

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	gossh "golang.org/x/crypto/ssh"
)

// bare makes an empty bare repository, served in process.
func bare(t *testing.T) string {
	t.Helper()
	UseInProcessFiles()
	dir := filepath.Join(t.TempDir(), "catalog.git")
	if _, err := git.PlainInit(dir, true); err != nil {
		t.Fatal(err)
	}
	return "file://" + dir
}

func open(t *testing.T, url string) *Repo {
	t.Helper()
	r, err := New(Options{URL: url, Cache: filepath.Join(t.TempDir(), "clone"), Committer: Person{Name: "rvav", Email: "rvav@corp.example"}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestRepo: an empty repository read, its first commit made, read by
// another clone, a commit on a branch that moved refused, then made on the
// new head; the last commit of a file (C1.1).
func TestRepo(t *testing.T) {
	ctx := context.Background()
	url := bare(t)
	a, b := open(t, url), open(t, url)
	if head, err := a.Check(ctx); err != nil || head != "" {
		t.Fatalf("check an empty repository = %q, %v", head, err)
	}
	snap, err := a.Read(ctx)
	if err != nil || snap.Head.SHA != "" || len(snap.Files) != 0 {
		t.Fatalf("empty read = %+v, %v", snap, err)
	}

	lea := Person{Name: "Léa Martin", Email: "lea.martin@corp.example"}
	first, err := a.Commit(ctx, "", []Change{{Path: a.FilePath("rhel9"), Content: []byte("slug: rhel9\n")}}, lea, "Publish rhel9 1.0")
	if err != nil || first.SHA == "" || first.Author != "Léa Martin" {
		t.Fatalf("first commit = %+v, %v", first, err)
	}
	if head, err := a.Check(ctx); err != nil || head != first.SHA {
		t.Fatalf("check = %q, %v", head, err)
	}

	// Another clone sees it, and commits on top.
	snap, err = b.Read(ctx)
	if err != nil || snap.Head.SHA != first.SHA || string(snap.Files["rhel9"].Content) != "slug: rhel9\n" ||
		snap.Files["rhel9"].Path != "catalog/rhel9.yaml" {
		t.Fatalf("b read = %+v, %v", snap, err)
	}
	second, err := b.Commit(ctx, first.SHA, []Change{{Path: b.FilePath("ubuntu"), Content: []byte("slug: ubuntu\n")}},
		Person{Name: "Thomas Nguyen", Email: "thomas.nguyen@corp.example"}, "Add ubuntu")
	if err != nil {
		t.Fatal(err)
	}

	// a's base is stale: refused, then made on the new head.
	if _, err := a.Commit(ctx, first.SHA, []Change{{Path: a.FilePath("rhel9"), Content: []byte("slug: rhel9\nv: 2\n")}}, lea,
		"Publish rhel9 1.1"); !errors.Is(err, ErrRejected) {
		t.Fatalf("commit on a moved branch = %v", err)
	}
	snap, err = a.Read(ctx)
	if err != nil || snap.Head.SHA != second.SHA || len(snap.Files) != 2 {
		t.Fatalf("a read again = %+v, %v", snap, err)
	}
	third, err := a.Commit(ctx, second.SHA, []Change{{Path: a.FilePath("rhel9"), Content: []byte("slug: rhel9\nv: 2\n")}}, lea,
		"Publish rhel9 1.1")
	if err != nil {
		t.Fatal(err)
	}
	// Nothing to change: no commit.
	if same, err := a.Commit(ctx, third.SHA, []Change{{Path: a.FilePath("rhel9"), Content: []byte("slug: rhel9\nv: 2\n")}}, lea,
		"Again"); err != nil || same.SHA != third.SHA {
		t.Fatalf("an empty commit = %+v, %v", same, err)
	}
	if c, err := a.LastCommit(ctx, "catalog/ubuntu.yaml"); err != nil || c.SHA != second.SHA || c.Message != "Add ubuntu" {
		t.Fatalf("ubuntu's last commit = %+v, %v", c, err)
	}

	// A deletion.
	if _, err := b.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Commit(ctx, third.SHA, []Change{{Path: b.FilePath("ubuntu")}}, lea, "Remove ubuntu"); err != nil {
		t.Fatal(err)
	}
	if snap, err = a.Read(ctx); err != nil || len(snap.Files) != 1 || snap.Files["ubuntu"].Slug != "" {
		t.Fatalf("after the deletion = %+v, %v", snap.Files, err)
	}
}

// TestNew: what a repository's URL and credential must be (C1.1).
func TestNew(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, err := gossh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	key := string(pem.EncodeToMemory(block))
	pub, _ := gossh.NewPublicKey(priv.Public())
	knownHost := "git.corp.example " + strings.TrimSpace(string(gossh.MarshalAuthorizedKey(pub)))
	for _, c := range []struct {
		o    Options
		fail string
	}{
		{Options{URL: "https://git.corp.example/infra/catalog.git", Credential: "glpat-x"}, ""},
		{Options{URL: "ssh://git@git.corp.example/infra/catalog.git", Credential: key, KnownHosts: knownHost}, ""},
		{Options{URL: "git@git.corp.example:infra/catalog.git", Credential: key, Insecure: true}, ""},
		{Options{URL: "git@git.corp.example:infra/catalog.git", Credential: key}, "host keys"},
		{Options{URL: "ssh://git@git.corp.example/x.git"}, "deploy key"},
		{Options{URL: "ssh://git@git.corp.example/x.git", Credential: "not a key", Insecure: true}, "deploy key"},
		{Options{URL: "ftp://git.corp.example/x.git"}, "an https"},
		{Options{URL: ""}, "URL"},
		{Options{URL: "https://git.corp.example/x.git", Dir: "../etc"}, "leaves"},
		{Options{URL: "https://oauth2:glpat-s3cret@git.corp.example/x.git"}, "holds a password"},
		{Options{URL: "https://rvav@git.corp.example/x.git", Credential: "glpat-x\n"}, ""},
		{Options{URL: "ssh://git@git.corp.example/infra/catalog.git", Credential: key + "\n\n", Insecure: true}, ""},
	} {
		_, err := New(c.o)
		if c.fail == "" && err != nil || c.fail != "" && (err == nil || !strings.Contains(err.Error(), c.fail)) {
			t.Errorf("%s: %v, want %q", c.o.URL, err, c.fail)
		}
	}

	// Outside tests, a local path is refused.
	inProcess.Lock()
	was := inProcess.on
	inProcess.on = false
	inProcess.Unlock()
	defer func() { inProcess.Lock(); inProcess.on = was; inProcess.Unlock() }()
	if _, err := New(Options{URL: "file:///srv/git/catalog.git"}); err == nil || !strings.Contains(err.Error(), "local path") {
		t.Errorf("a local path: %v", err)
	}
	if err := CheckURL("/srv/git/catalog.git"); err == nil {
		t.Error("a bare path is a URL")
	}
}

// TestRepoCache: one cache for several repositories and branches never
// mixes them: an empty repository read after another is empty, and its
// first commit holds nothing of the other; a branch deleted on the server
// starts again with nothing of the clone's index; a file too large isn't
// read (C1 review).
func TestRepoCache(t *testing.T) {
	ctx := context.Background()
	cache := filepath.Join(t.TempDir(), "cache")
	at := func(url, branch string) *Repo {
		r, err := New(Options{URL: url, Branch: branch, Cache: cache})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	urlA, urlB := bare(t), bare(t)
	a := at(urlA, "main")
	first, err := a.Commit(ctx, "", []Change{{Path: a.FilePath("rhel9"), Content: []byte("slug: rhel9\n")}}, Person{}, "A's item")
	if err != nil {
		t.Fatal(err)
	}
	b := at(urlB, "main")
	snap, err := b.Read(ctx)
	if err != nil || snap.Head.SHA != "" || len(snap.Files) != 0 {
		t.Fatalf("B read after A = %+v, %v", snap, err)
	}
	if _, err := b.Commit(ctx, "", []Change{{Path: b.FilePath("ubuntu"), Content: []byte("slug: ubuntu\n")}}, Person{}, "B's item"); err != nil {
		t.Fatal(err)
	}
	snap, _ = at(urlB, "main").Read(ctx)
	if len(snap.Files) != 1 || snap.Files["ubuntu"].Slug == "" {
		t.Fatalf("B's files = %v", snap.Files)
	}
	if c, _ := at(urlB, "main").LastCommit(ctx, "catalog/rhel9.yaml"); c.SHA != "" {
		t.Fatalf("A's history is in B: %+v", c)
	}

	// The branch deleted on the server: started again, on its own.
	bareA, err := git.PlainOpen(strings.TrimPrefix(urlA, "file://"))
	if err != nil {
		t.Fatal(err)
	}
	if err := bareA.Storer.RemoveReference(plumbing.NewBranchReferenceName("main")); err != nil {
		t.Fatal(err)
	}
	if snap, err := a.Read(ctx); err != nil || snap.Head.SHA != "" || len(snap.Files) != 0 {
		t.Fatalf("A without its branch = %+v, %v", snap, err)
	}
	again, err := a.Commit(ctx, "", []Change{{Path: a.FilePath("windows"), Content: []byte("slug: windows\n")}}, Person{}, "Anew")
	if err != nil || again.SHA == first.SHA {
		t.Fatal(err)
	}
	c, err := bareA.CommitObject(plumbing.NewHash(again.SHA))
	if err != nil {
		t.Fatal(err)
	}
	files, _ := c.Files()
	var names []string
	_ = files.ForEach(func(f *object.File) error { names = append(names, f.Name); return nil })
	if c.NumParents() != 0 || len(names) != 1 || names[0] != "catalog/windows.yaml" {
		t.Fatalf("the new branch's commit: %d parents, files %v", c.NumParents(), names)
	}

	// A file over the limit isn't read.
	huge := bytes.Repeat([]byte("x"), MaxFileSize+1)
	if _, err := a.Commit(ctx, again.SHA, []Change{{Path: a.FilePath("huge"), Content: huge}}, Person{}, "Huge"); err != nil {
		t.Fatal(err)
	}
	snap, _ = a.Read(ctx)
	if f := snap.Files["huge"]; !f.TooLarge || f.Content != nil || f.Hash == "" || snap.Files["windows"].TooLarge {
		t.Fatalf("the huge file = %v %d", f.TooLarge, len(f.Content))
	}
}
