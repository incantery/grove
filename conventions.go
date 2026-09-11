// The conventions file: what a fresh checkout of THIS repo needs.
//
// A repo's conventions are facts about the repo — `.env` is where its
// secrets are, `node_modules` is too heavy to have twice — so they
// live in the repo, in `grove.toml` at its root, and travel with it.
// A person's own additions for every repo go in
// $XDG_CONFIG_HOME/grove/grove.toml.
//
// The reader is deliberately small: two string lists, `copy` and
// `link`, at the top level. It also reads the `[worktree]` table of a
// `rook.toml` at the repo root, which is where vera's fleet and rook
// kept the same two lists before grove existed, so nothing has to
// move on the day grove arrives.
package grove

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LoadConventions reads the repo's conventions from grove.toml at
// root, else from rook.toml's [worktree] table there. A repo with
// neither has none, which is not an error.
func LoadConventions(root string) Conventions {
	if c, ok := readConventions(filepath.Join(root, "grove.toml"), ""); ok {
		return c
	}
	c, _ := readConventions(filepath.Join(root, "rook.toml"), "[worktree]")
	return c
}

// UserConventions reads the person's own, for every repo.
func UserConventions() Conventions {
	dir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Conventions{}
		}
		dir = filepath.Join(home, ".config")
	}
	c, _ := readConventions(filepath.Join(dir, "grove", "grove.toml"), "")
	return c
}

// readConventions reads `copy` and `link` from the file, under the
// named section ("" is the top level). Anything else is skipped.
func readConventions(path, section string) (Conventions, bool) {
	var c Conventions
	b, err := os.ReadFile(path)
	if err != nil {
		return c, false
	}
	in := ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if strings.HasPrefix(line, "[") {
			in = line
			continue
		}
		if in != section {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "copy":
			c.Copy = tomlStrings(strings.TrimSpace(val))
		case "link":
			c.Link = tomlStrings(strings.TrimSpace(val))
		}
	}
	return c, true
}

func tomlStrings(v string) []string {
	v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
	var out []string
	for _, s := range strings.Split(v, ",") {
		s = strings.TrimSpace(s)
		if u, err := strconv.Unquote(s); err == nil {
			s = u
		} else {
			s = strings.Trim(s, `"'`)
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
