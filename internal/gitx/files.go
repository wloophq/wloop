package gitx

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// gitDir is the per-worktree git directory of the checkout at dir.
func gitDir(dir string) string {
	path := filepath.Join(dir, ".git")
	b, err := os.ReadFile(path)
	if err != nil {
		return path
	}
	path = strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return path
}

func isOid(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// ReadHead reads the branch and the commit HEAD of the checkout at dir names
// from the repository files. branch is empty for a detached HEAD. ok is false
// when the files do not answer: a reftable repository, a symbolic ref or any
// unexpected content, so the caller asks git.
func ReadHead(dir string) (oid, branch string, ok bool) {
	if dir == "" {
		return "", "", false
	}
	gd := gitDir(dir)
	b, err := os.ReadFile(filepath.Join(gd, "HEAD"))
	if err != nil {
		return "", "", false
	}
	head := strings.TrimSpace(string(b))
	ref, symbolic := strings.CutPrefix(head, "ref: ")
	if !symbolic {
		return head, "", isOid(head)
	}
	branch, isBranch := strings.CutPrefix(ref, "refs/heads/")
	if !isBranch {
		return "", "", false
	}
	oid, found := readRef(commonDir(dir), ref)
	if !found {
		return "", "", false
	}
	return oid, branch, true
}

// readRef resolves a ref from the loose file, then packed-refs. found is
// false when the files cannot say, which includes a ref that does not exist
// in a repository whose layout is not the files backend.
func readRef(common, ref string) (oid string, found bool) {
	if fi, err := os.Stat(filepath.Join(common, "reftable")); err == nil && fi.IsDir() {
		return "", false
	}
	if b, err := os.ReadFile(filepath.Join(common, filepath.FromSlash(ref))); err == nil {
		oid = strings.TrimSpace(string(b))
		return oid, isOid(oid)
	}
	f, err := os.Open(filepath.Join(common, "packed-refs"))
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		if sha, name, ok := strings.Cut(line, " "); ok && name == ref && isOid(sha) {
			return sha, true
		}
	}
	return "", false
}

// refAbsent reports that no loose file and no packed-refs entry holds ref, in
// a repository that keeps ref files.
func refAbsent(common, ref string) bool {
	if fi, err := os.Stat(filepath.Join(common, "reftable")); err == nil && fi.IsDir() {
		return false
	}
	if _, err := os.Stat(filepath.Join(common, filepath.FromSlash(ref))); err == nil {
		return false
	}
	f, err := os.Open(filepath.Join(common, "packed-refs"))
	if err != nil {
		return os.IsNotExist(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if _, name, ok := strings.Cut(sc.Text(), " "); ok && name == ref {
			return false
		}
	}
	return sc.Err() == nil
}

var (
	headerShape = regexp.MustCompile(`^\[[A-Za-z0-9.-]+( "[^"\\]*")?\]$`)
	namedHeader = regexp.MustCompile(`^\[(branch|remote) "([^"\\]*)"\]$`)
)

// ReadUpstream reads the remote branch the current branch of the checkout at
// dir tracks from the repository files; branch is empty when it tracks none.
// ok is false whenever the files cannot answer with certainty, so the caller
// asks git: includes, worktree config, any syntax the parser does not fully
// understand (comments, continuations, quotes, unusual headers), a remote
// without exactly the standard fetch refspec, or a layout other than loose and
// packed refs. Only the repository's own config is read: branch settings in
// the user or system config are not seen.
func ReadUpstream(dir string) (branch string, ok bool) {
	if dir == "" {
		return "", false
	}
	_, current, ok := ReadHead(dir)
	if !ok {
		return "", false
	}
	if current == "" {
		return "", true
	}
	common := commonDir(dir)
	b, err := os.ReadFile(filepath.Join(common, "config"))
	if err != nil {
		return "", false
	}
	var remote, merge string
	var merges int
	fetches := map[string][]string{}
	kind, name := "", ""
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		if strings.HasSuffix(line, "\\") || strings.Contains(lower, "worktreeconfig") || strings.HasPrefix(lower, "[include") {
			return "", false
		}
		if strings.HasPrefix(line, "[") {
			kind, name = "", ""
			if m := namedHeader.FindStringSubmatch(line); m != nil {
				kind, name = m[1], m[2]
			} else if strings.HasPrefix(lower, "[branch") || strings.HasPrefix(lower, "[remote") || !headerShape.MatchString(line) {
				return "", false
			}
			continue
		}
		if kind == "" || line == "" {
			continue
		}
		relevant := (kind == "branch" && name == current) || kind == "remote"
		if !relevant {
			continue
		}
		if strings.ContainsAny(line, "#;\"") {
			return "", false
		}
		key, value, has := strings.Cut(line, "=")
		if !has {
			return "", false
		}
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		switch {
		case kind == "branch" && key == "remote":
			remote = value
		case kind == "branch" && key == "merge":
			merge = value
			merges++
		case kind == "remote" && key == "fetch":
			fetches[name] = append(fetches[name], value)
		}
	}
	if merges > 1 {
		return "", false
	}
	short, isBranch := strings.CutPrefix(merge, "refs/heads/")
	if remote == "" || merges == 0 {
		return "", true
	}
	if remote == "." {
		return "", isBranch
	}
	if !isBranch || len(fetches[remote]) != 1 || fetches[remote][0] != "+refs/heads/*:refs/remotes/"+remote+"/*" {
		return "", false
	}
	tracking := "refs/remotes/" + remote + "/" + short
	if _, found := readRef(common, tracking); found {
		return short, true
	}
	return "", refAbsent(common, tracking)
}

// readBranchTip reads the commit of a local branch. absent is true when the
// files show no such branch.
func readBranchTip(dir, branch string) (oid string, absent, ok bool) {
	if dir == "" {
		return "", false, false
	}
	common := commonDir(dir)
	ref := "refs/heads/" + branch
	if oid, found := readRef(common, ref); found {
		return oid, false, true
	}
	if refAbsent(common, ref) {
		if _, err := os.Stat(filepath.Join(common, "HEAD")); err == nil {
			return "", true, true
		}
	}
	return "", false, false
}
