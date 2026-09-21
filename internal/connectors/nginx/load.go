package nginx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type mapping struct{ from, to string }

// skip records an include that was not followed, so an empty result can say why.
type skip struct {
	file    string
	line    int
	pattern string
	reason  string
}

var (
	errNotFound = errors.New("not found")
	errLoop     = errors.New("symlink loop")
	errOutside  = errors.New("outside the mounted paths")
	errNotFile  = errors.New("not a regular file")
	errDir      = errors.New("not a regular file")
)

func skippable(err error) bool {
	return errors.Is(err, errNotFound) || errors.Is(err, errLoop) || errors.Is(err, errOutside) || errors.Is(err, errNotFile) || errors.Is(err, errDir)
}

// frame is one file on the include stack; at is the line of the include in
// it that is being expanded, for cycle messages.
type frame struct {
	real, disp string
	at         int
}

type loader struct {
	ctx       context.Context
	lim       limits
	prefix    string
	entryRoot string
	roots     []string
	maps      []mapping
	cache     map[string][]*directive
	bytes     int64
	files     int
	includes  int
	count     int
	skipped   []skip
}

// load reads the entry config and everything it includes into one directive
// tree. Only files whose symlink-free path lies inside the mounted roots are
// ever opened.
func load(ctx context.Context, s settings, lim limits) (*loader, []*directive, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	l := &loader{ctx: ctx, lim: lim, cache: map[string][]*directive{}}
	real, err := filepath.EvalSymlinks(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("nginx: %s does not exist", s.path)
		}
		return nil, nil, fmt.Errorf("nginx: cannot open %s: %v", s.path, cause(err))
	}
	fi, err := os.Stat(real)
	if err != nil {
		return nil, nil, fmt.Errorf("nginx: cannot open %s: %v", s.path, cause(err))
	}
	l.entryRoot = real
	if !fi.IsDir() {
		l.entryRoot = filepath.Dir(real)
	}
	if systemRoot(l.entryRoot) {
		return nil, nil, errSystemRoot
	}
	l.prefix = l.entryRoot
	l.roots = []string{l.entryRoot}
	for _, m := range s.maps {
		to, err := filepath.EvalSymlinks(m.to)
		if err != nil {
			return nil, nil, fmt.Errorf("path_map target %s does not exist", m.to)
		}
		if st, err := os.Stat(to); err != nil || !st.IsDir() {
			return nil, nil, fmt.Errorf("path_map target %s is not a directory", m.to)
		}
		if systemRoot(to) {
			return nil, nil, fmt.Errorf("path_map target %s must not be a system root", m.to)
		}
		l.maps = append(l.maps, mapping{m.from, to})
		l.roots = append(l.roots, to)
	}
	entries := []string{real}
	switch {
	case fi.IsDir():
		if entries, err = l.entries(real); err != nil {
			return nil, nil, err
		}
	case !fi.Mode().IsRegular():
		return nil, nil, fmt.Errorf("nginx: %s is not a regular file", s.path)
	}
	var tree []*directive
	for _, e := range entries {
		ds, err := l.read(e, nil)
		if err != nil {
			return nil, nil, err
		}
		ds, err = l.expand(ds, []frame{{real: e, disp: l.display(e)}})
		if err != nil {
			return nil, nil, err
		}
		tree = append(tree, ds...)
	}
	return l, tree, nil
}

// entries picks the files a directory mount starts from: nginx.conf, else its
// *.conf fragments, else every plain file (a mounted sites-enabled).
func (l *loader) entries(dir string) ([]string, error) {
	if r, err := l.candidate(filepath.Join(dir, "nginx.conf")); err == nil {
		return []string{r}, nil
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("nginx: cannot list %s: %v", dir, cause(err))
	}
	var confs, others []string
	for _, de := range des {
		switch n := de.Name(); {
		case strings.HasPrefix(n, "."):
		case strings.HasSuffix(n, ".conf"):
			confs = append(confs, n)
		default:
			others = append(others, n)
		}
	}
	names := confs
	if len(names) == 0 {
		names = others
	}
	var out []string
	for _, n := range names {
		r, err := l.candidate(filepath.Join(dir, n))
		switch {
		case err == nil:
			out = append(out, r)
		case errors.Is(err, errDir):
		case skippable(err):
			l.skipped = append(l.skipped, skip{file: n, reason: err.Error()})
		default:
			return nil, fmt.Errorf("nginx: cannot read %s: %v", n, cause(err))
		}
	}
	if len(out) == 0 && len(l.skipped) == 0 {
		return nil, fmt.Errorf("nginx: no nginx.conf or *.conf files in %s", dir)
	}
	return out, nil
}

// candidate resolves p and admits it only as a regular file inside the roots.
func (l *loader) candidate(p string) (string, error) {
	r, err := l.resolve(p)
	if err != nil {
		return "", err
	}
	if !l.within(r) {
		return "", errOutside
	}
	fi, err := os.Lstat(r)
	switch {
	case err != nil:
		return "", err
	case fi.IsDir():
		return "", errDir
	case !fi.Mode().IsRegular():
		return "", errNotFile
	}
	return r, nil
}

// resolve walks p one component at a time like the kernel does, so every
// symlink is seen. Absolute link targets are nginx-side paths and go through
// path_map, which is what keeps Debian's absolute sites-enabled links working.
func (l *loader) resolve(p string) (string, error) {
	rest := strings.Split(filepath.ToSlash(p), "/")
	sep := string(filepath.Separator)
	cur := sep
	hops := 0
	for len(rest) > 0 {
		c := rest[0]
		rest = rest[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, c)
		fi, err := os.Lstat(next)
		if err != nil {
			// Failing outside the roots says nothing about the mount; report
			// where the path points rather than what the host happens to have.
			if !l.within(cur) {
				return "", errOutside
			}
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
				return "", errNotFound
			}
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			cur = next
			continue
		}
		if hops++; hops > l.lim.symlinkHops {
			return "", errLoop
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			target = l.mapPath(target)
			cur = sep
		}
		rest = append(strings.Split(filepath.ToSlash(target), "/"), rest...)
	}
	return cur, nil
}

// mapPath rewrites an nginx-side absolute path to where it is mounted here.
// Maps are sorted longest nginx prefix first.
func (l *loader) mapPath(p string) string {
	p = filepath.Clean(p)
	for _, m := range l.maps {
		switch {
		case m.from == "/":
			return filepath.Join(m.to, p)
		case p == m.from, strings.HasPrefix(p, m.from+"/"):
			return filepath.Join(m.to, p[len(m.from):])
		}
	}
	return p
}

func (l *loader) within(p string) bool {
	for _, r := range l.roots {
		if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (l *loader) display(p string) string {
	if p != l.entryRoot && strings.HasPrefix(p, l.entryRoot+string(filepath.Separator)) {
		if r, err := filepath.Rel(l.entryRoot, p); err == nil {
			return filepath.ToSlash(r)
		}
	}
	return p
}

func cause(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

func dirErr(d *directive, msg string) error {
	return fmt.Errorf("nginx: %s:%d: %s", d.file, d.line, msg)
}

// read parses one file once per scan. site is the include that reached it,
// nil for an entry file; limit errors name the site.
func (l *loader) read(real string, site *directive) ([]*directive, error) {
	if ds, ok := l.cache[real]; ok {
		return ds, nil
	}
	if err := l.ctx.Err(); err != nil {
		return nil, err
	}
	subject := "file"
	fail := func(msg string) error { return fmt.Errorf("nginx: %s: %s", l.display(real), msg) }
	if site != nil {
		subject = l.display(real)
		fail = func(msg string) error { return dirErr(site, msg) }
	}
	tooBig := fmt.Sprintf("%s is larger than %d bytes", subject, l.lim.fileBytes)
	fi, err := os.Stat(real)
	if err != nil {
		return nil, fail(fmt.Sprintf("cannot read %s: %v", subject, cause(err)))
	}
	if !fi.Mode().IsRegular() {
		return nil, fail(subject + " is not a regular file")
	}
	if fi.Size() > l.lim.fileBytes {
		return nil, fail(tooBig)
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, fail(fmt.Sprintf("cannot read %s: %v", subject, cause(err)))
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, l.lim.fileBytes+1))
	if err != nil {
		return nil, fail(fmt.Sprintf("cannot read %s: %v", subject, cause(err)))
	}
	if int64(len(b)) > l.lim.fileBytes {
		return nil, fail(tooBig)
	}
	if l.files++; l.files > l.lim.files {
		return nil, fail(fmt.Sprintf("more than %d config files", l.lim.files))
	}
	if l.bytes += int64(len(b)); l.bytes > l.lim.totalBytes {
		return nil, fail(fmt.Sprintf("config files exceed %d bytes in total", l.lim.totalBytes))
	}
	ds, err := parse(b, l.display(real), l.lim)
	if err != nil {
		return nil, err
	}
	l.cache[real] = ds
	return ds, nil
}

// expand splices included files in place of their include directives. It
// copies rather than mutates, because one cached file may be included from
// many places (SWAG includes proxy.conf in every location).
func (l *loader) expand(ds []*directive, stack []frame) ([]*directive, error) {
	out := make([]*directive, 0, len(ds))
	for _, d := range ds {
		if l.count++; l.count > l.lim.directives {
			return nil, dirErr(d, fmt.Sprintf("more than %d directives", l.lim.directives))
		}
		switch {
		case d.name == "include":
			files, err := l.include(d)
			if err != nil {
				return nil, err
			}
			for _, f := range files {
				if err := l.ctx.Err(); err != nil {
					return nil, err
				}
				if l.includes++; l.includes > l.lim.includes {
					return nil, dirErr(d, fmt.Sprintf("more than %d include expansions", l.lim.includes))
				}
				next := make([]frame, len(stack), len(stack)+1)
				copy(next, stack)
				next[len(next)-1].at = d.line
				for i, fr := range next {
					if fr.real != f {
						continue
					}
					var chain []string
					for _, c := range next[i:] {
						chain = append(chain, fmt.Sprintf("%s:%d", c.disp, c.at))
					}
					return nil, dirErr(d, "include cycle: "+strings.Join(append(chain, fr.disp), " -> "))
				}
				if len(stack) >= l.lim.includeDepth {
					return nil, dirErr(d, fmt.Sprintf("includes nested deeper than %d", l.lim.includeDepth))
				}
				sub, err := l.read(f, d)
				if err != nil {
					return nil, err
				}
				if sub, err = l.expand(sub, append(next, frame{real: f, disp: l.display(f)})); err != nil {
					return nil, err
				}
				out = append(out, sub...)
			}
		case d.block != nil:
			children, err := l.expand(d.block, stack)
			if err != nil {
				return nil, err
			}
			nd := *d
			nd.block = children
			out = append(out, &nd)
		default:
			out = append(out, d)
		}
	}
	return out, nil
}

// include returns the files an include directive names, in nginx's order.
// Paths nginx would find but this mount cannot (SWAG's image-only
// /etc/nginx/mime.types) are recorded and skipped rather than fatal.
func (l *loader) include(d *directive) ([]string, error) {
	if len(d.args) != 1 {
		return nil, dirErr(d, "include expects 1 argument")
	}
	pattern := d.args[0]
	skipped := func(err error) ([]string, error) {
		if !skippable(err) {
			return nil, dirErr(d, fmt.Sprintf("cannot read included path: %v", cause(err)))
		}
		l.skipped = append(l.skipped, skip{file: d.file, line: d.line, pattern: pattern, reason: err.Error()})
		return nil, nil
	}
	if !strings.ContainsAny(pattern, "*?[") {
		r, err := l.candidate(l.local(pattern))
		if err != nil {
			return skipped(err)
		}
		return []string{r}, nil
	}
	static, rest := splitGlob(filepath.Clean(pattern))
	base, err := l.resolve(l.local(static))
	switch {
	case errors.Is(err, errNotFound):
		return nil, nil
	case err == nil && !l.within(base):
		err = errOutside
	}
	if err != nil {
		return skipped(err)
	}
	matches, err := filepath.Glob(filepath.Join(globEscape(base), rest))
	if err != nil {
		return nil, dirErr(d, "invalid include pattern")
	}
	sort.Strings(matches)
	dotted := strings.HasPrefix(filepath.Base(pattern), ".")
	var out []string
	for _, m := range matches {
		if !dotted && strings.HasPrefix(filepath.Base(m), ".") {
			continue
		}
		r, err := l.candidate(m)
		if err != nil {
			if _, err = skipped(err); err != nil {
				return nil, err
			}
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// local turns an nginx-side include path into a path on this side: relative
// paths resolve against the main config's directory, as nginx's prefix does.
func (l *loader) local(p string) string {
	if filepath.IsAbs(p) {
		return l.mapPath(p)
	}
	return filepath.Join(l.prefix, p)
}

// splitGlob separates the components before the first wildcard, which must
// resolve inside the roots before any directory is listed.
func splitGlob(p string) (static, rest string) {
	parts := strings.Split(p, "/")
	for i, c := range parts {
		if strings.ContainsAny(c, "*?[") {
			static = strings.Join(parts[:i], "/")
			if static == "" && strings.HasPrefix(p, "/") {
				static = "/"
			}
			return static, strings.Join(parts[i:], "/")
		}
	}
	return p, ""
}

func globEscape(p string) string {
	if filepath.Separator != '/' {
		return p
	}
	var b strings.Builder
	for _, r := range p {
		if strings.ContainsRune(`*?[\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
