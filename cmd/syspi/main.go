// SPDX-License-Identifier: Apache-2.0

// Command syspi is the SysML Package Installer: it resolves, verifies,
// installs and locks KerML / SysML v2 project-interchange packages.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/Open-MBEE/SysPI/internal/index"
	"github.com/Open-MBEE/SysPI/internal/install"
	"github.com/Open-MBEE/SysPI/internal/origin"
	"github.com/Open-MBEE/SysPI/internal/project"
	"github.com/Open-MBEE/SysPI/internal/vcs"
	"github.com/Open-MBEE/SysPI/internal/version"
)

// Version is set by the release build (-ldflags "-X main.Version=v1.2.3").
var Version = "dev"

const usageText = `syspi — SysML Package Installer

Usage:
  syspi <command> [flags] [arguments]

Commands:
  init      write .project.json and .meta.json for a new package
  add       add a usage to .project.json, then resolve, install and lock
  install   resolve .project.json and the lock, install what is missing or changed
  sync      same as install
  lock      resolve and write .project.lock.json without installing
  bundle    build a deterministic .kpar from a project directory
  index     emit a static sysand-compatible index (syspi index emit)
  version   print the version

A package is any git, svn or hg repository with .project.json at a tag:
  pkg:<vcs>/<host>/<path>[@<version>][#<subpath>]
Run "syspi <command> -h" for the flags of a command.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "-help" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, usageText)
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var err error
	switch args[0] {
	case "init":
		err = cmdInit(args[1:], stdout, stderr)
	case "add":
		err = cmdAdd(ctx, args[1:], stdout, stderr)
	case "install", "sync":
		err = cmdInstall(ctx, args[0], args[1:], stdout, stderr, false)
	case "lock":
		err = cmdInstall(ctx, args[0], args[1:], stdout, stderr, true)
	case "bundle":
		err = cmdBundle(ctx, args[1:], stdout, stderr)
	case "index":
		err = cmdIndex(args[1:], stdout, stderr)
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, "syspi", Version)
	default:
		fmt.Fprintf(stderr, "syspi: unknown command %q\n\n%s", args[0], usageText)
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		if errors.Is(err, errUsage) {
			return 2
		}
		fmt.Fprintf(stderr, "syspi: %v\n", err)
		return 1
	}
	return 0
}

var errUsage = errors.New("usage")

// commonFlags are the flags every project command takes.
type commonFlags struct {
	dir     string
	index   string
	offline bool
	quiet   bool
}

func (c *commonFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.dir, "C", ".", "project directory (holding .project.json)")
	fs.StringVar(&c.index, "index", os.Getenv("SYSPI_INDEX"), "sysand static index URL or directory for pkg:sysand/ resources (default $SYSPI_INDEX)")
	fs.BoolVar(&c.offline, "offline", false, "never contact a repository or server; the lock and lib/ must already satisfy the project")
	fs.BoolVar(&c.quiet, "q", false, "print only errors")
}

func (c *commonFlags) options(stderr io.Writer, lockOnly bool) install.Options {
	log := stderr
	if c.quiet {
		log = io.Discard
	}
	return install.Options{Root: c.dir, Index: c.index, Offline: c.offline, Log: log, LockOnly: lockOnly}
}

func newFlagSet(name string, stderr io.Writer, synopsis string) *flag.FlagSet {
	fs := flag.NewFlagSet("syspi "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: syspi %s %s\n", name, synopsis)
		fs.PrintDefaults()
	}
	return fs
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errUsage
	}
	return nil
}

func cmdInit(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("init", stderr, "[-C dir] [-publisher P] [-name N] [-version V]")
	dir := fs.String("C", ".", "directory to initialise")
	publisher := fs.String("publisher", "", "publisher (the first part of the install name <publisher>-<name>_<version>)")
	name := fs.String("name", "", "project name (default: the directory name)")
	ver := fs.String("version", "0.1.0", "initial version")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return errUsage
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(abs, project.ProjectFile)); err == nil {
		return fmt.Errorf("%s already exists in %s", project.ProjectFile, *dir)
	}
	if *name == "" {
		*name = filepath.Base(abs)
	}
	p := &project.Project{Name: *name, Publisher: *publisher, Version: *ver}
	if err := p.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	if err := p.Write(abs); err != nil {
		return err
	}
	m, err := project.GenerateMeta(abs, project.CreatedTime())
	if err != nil {
		return err
	}
	if err := m.Write(abs); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "initialised %s in %s (%s, %s)\n", p.InstallName(), *dir, project.ProjectFile, project.MetaFile)
	return nil
}

func cmdAdd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var c commonFlags
	fs := newFlagSet("add", stderr, "[flags] <resource>[@<version>] [<versionConstraint>]")
	c.bind(fs)
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		fs.Usage()
		return errUsage
	}
	resource := fs.Arg(0)
	constraint := ""
	if fs.NArg() == 2 {
		constraint = fs.Arg(1)
	}
	o, err := origin.Parse(resource)
	if err != nil {
		return err
	}
	if constraint != "" {
		if _, err := version.ParseConstraint(constraint); err != nil {
			return err
		}
		if o.Version != "" {
			return fmt.Errorf("%s names version %s and a constraint %q was given; use one", resource, o.Version, constraint)
		}
	}
	p, err := project.ReadProject(c.dir)
	if err != nil {
		return err
	}
	id := o.Identity()
	for i, u := range p.Usage {
		uo, err := origin.Parse(u.Resource)
		if err == nil && uo.Identity() == id {
			p.Usage[i] = project.Usage{Resource: resource, VersionConstraint: constraint}
			fmt.Fprintf(stderr, "replacing usage %q\n", u.Resource)
			p.Usage = append(p.Usage[:i], p.Usage[i+1:]...)
			break
		}
	}
	p.Usage = append(p.Usage, project.Usage{Resource: resource, VersionConstraint: constraint})
	if err := p.Validate(); err != nil {
		return err
	}
	// Resolve against a copy first so a failing add leaves .project.json unchanged.
	data, err := os.ReadFile(filepath.Join(c.dir, project.ProjectFile))
	if err != nil {
		return err
	}
	if err := p.Write(c.dir); err != nil {
		return err
	}
	res, err := install.Run(ctx, c.options(stderr, false))
	if err != nil {
		os.WriteFile(filepath.Join(c.dir, project.ProjectFile), data, 0o644)
		return err
	}
	report(stdout, res)
	return refreshMeta(c.dir)
}

func cmdInstall(ctx context.Context, name string, args []string, stdout, stderr io.Writer, lockOnly bool) error {
	var c commonFlags
	fs := newFlagSet(name, stderr, "[flags]")
	c.bind(fs)
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return errUsage
	}
	res, err := install.Run(ctx, c.options(stderr, lockOnly))
	if err != nil {
		return err
	}
	report(stdout, res)
	return nil
}

func report(w io.Writer, res *install.Result) {
	n := len(res.Lock.Packages)
	fmt.Fprintf(w, "%d package(s) locked: %d installed, %d already present, %d not installed (standard libraries and local directories)", n, len(res.Installed), len(res.Reused), len(res.Skipped))
	if res.LockWritten {
		fmt.Fprintf(w, "; %s updated", project.LockFile)
	}
	fmt.Fprintln(w)
}

// refreshMeta regenerates .meta.json when the project has one, so its
// checksum of .project.json stays true after a usage was added.
func refreshMeta(dir string) error {
	if _, ok, err := project.ReadMeta(dir); err != nil || !ok {
		return err
	}
	m, err := project.GenerateMeta(dir, project.CreatedTime())
	if err != nil {
		return err
	}
	return m.Write(dir)
}

func cmdBundle(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("bundle", stderr, "[-o out.kpar] [-tag <tag>] <dir>")
	out := fs.String("o", "", "output path (default <publisher>-<name>_<version>.kpar in the current directory)")
	tag := fs.String("tag", "", "tag to check against .project.json's version (default: the checked-out git/hg tag of <dir>, when any)")
	if err := parse(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errUsage
	}
	dir := fs.Arg(0)
	p, err := project.ReadProject(dir)
	if err != nil {
		return err
	}
	if *tag == "" {
		*tag = vcs.CheckedOutTag(ctx, dir)
	}
	if *tag != "" {
		tv, _, err := version.ParseTag(*tag)
		if err != nil {
			return fmt.Errorf("tag %q: %v", *tag, err)
		}
		pv, _ := version.Parse(p.Version)
		if !tv.Equal(pv) {
			return fmt.Errorf("tag %s carries version %s but %s says \"version\": %q; the tag must equal the version", *tag, tv, project.ProjectFile, p.Version)
		}
	}
	if *out == "" {
		*out = p.InstallName() + project.ArchiveExt
	}
	meta, err := project.Bundle(dir, *out, createdFor(ctx, dir))
	if err != nil {
		return err
	}
	a, err := project.OpenArchive(*out)
	if err != nil {
		return fmt.Errorf("verifying the bundle: %v", err)
	}
	a.Close()
	fmt.Fprintf(stdout, "wrote %s (%d members, sha256 %s, created %s)\n", *out, len(a.Members), a.SHA256, meta.Created)
	return nil
}

// createdFor is the "created" time of a bundle: SOURCE_DATE_EPOCH when set,
// else the commit time of the checked-out revision, else now — so a bundle
// of an unchanged checkout is byte-identical across runs.
func createdFor(ctx context.Context, dir string) time.Time {
	if os.Getenv("SOURCE_DATE_EPOCH") != "" {
		return project.CreatedTime()
	}
	if t, ok := vcs.CheckedOutTime(ctx, dir); ok {
		return t
	}
	return project.CreatedTime()
}

func cmdIndex(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "emit" {
		fmt.Fprintln(stderr, "Usage: syspi index emit [-o <index dir>] <package dir or .kpar>...")
		return errUsage
	}
	fs := newFlagSet("index emit", stderr, "[-o <index dir>] <package dir or .kpar>...")
	out := fs.String("o", "index", "directory to write the index into")
	if err := parse(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return errUsage
	}
	iris, err := index.Emit(*out, fs.Args(), func(dir string) time.Time { return createdFor(context.Background(), dir) })
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s: %s\n", *out, strings.Join(iris, ", "))
	return nil
}
