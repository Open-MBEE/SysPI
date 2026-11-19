// SPDX-License-Identifier: Apache-2.0

package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Open-MBEE/SysPI/internal/lock"
	"github.com/Open-MBEE/SysPI/internal/origin"
	"github.com/Open-MBEE/SysPI/internal/project"
)

type ModelCheckInputs struct {
	Project  []string
	Direct   []string
	All      []string
	Packages []*ModelPackage
}

// ModelPackage holds staged sources and the direct edges from the verified lock.
type ModelPackage struct {
	Resource string
	Files    []string
	direct   []*ModelPackage
}

// Scope returns each reachable source once, keeping direct dependencies first.
func (p *ModelPackage) Scope() ModelCheckInputs {
	inputs := ModelCheckInputs{Project: p.Files}
	inputs.All = append(inputs.All, p.Files...)
	seen := map[*ModelPackage]bool{p: true}
	add := func(deps []*ModelPackage) {
		for _, dep := range deps {
			if seen[dep] {
				continue
			}
			seen[dep] = true
			inputs.Packages = append(inputs.Packages, dep)
			inputs.All = append(inputs.All, dep.Files...)
		}
	}
	add(p.direct)
	inputs.Direct = inputs.All[len(p.Files):]
	for i := 0; i < len(inputs.Packages); i++ {
		add(inputs.Packages[i].direct)
	}
	return inputs
}

// ModelInputs supplies the project's sources and its exact verified lock closure.
func ModelInputs(root, scratch string) (ModelCheckInputs, error) {
	var inputs ModelCheckInputs
	root, err := filepath.Abs(root)
	if err != nil {
		return inputs, err
	}
	p, err := project.ReadProject(root)
	if err != nil {
		return inputs, err
	}
	files, err := copyTextModels(root, filepath.Join(scratch, "project"))
	if err != nil {
		return inputs, err
	}
	if len(files) == 0 {
		return inputs, fmt.Errorf("%s: model check requires at least one .sysml or .kerml file", root)
	}
	model, err := modelGraph(root, scratch, p)
	if err != nil {
		return inputs, err
	}
	model.Files = files
	return model.Scope(), nil
}

// DependencyModels exports only the verified dependencies in the project's lock closure.
func DependencyModels(root, scratch string) ([]string, error) {
	p, err := project.ReadProject(root)
	if err != nil {
		return nil, err
	}
	model, err := modelGraph(root, scratch, p)
	if err != nil {
		return nil, err
	}
	return model.Scope().All, nil
}

func modelGraph(root, scratch string, p *project.Project) (*ModelPackage, error) {
	model := &ModelPackage{}
	if len(p.Usage) == 0 {
		return model, nil
	}
	l, ok, err := lock.Read(root, project.LockFile)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%s: no %s; run syspi install", root, project.LockFile)
	}
	if discrepancies := verifyLock(root, p, l); len(discrepancies) > 0 {
		return nil, fmt.Errorf("model check requires a verified installation: %s", discrepancies[0])
	}
	type edge struct {
		from     *ModelPackage
		resource string
	}
	queue := make([]edge, 0, len(p.Usage))
	for _, u := range p.Usage {
		queue = append(queue, edge{model, u.Resource})
	}
	packages := make(map[string]*ModelPackage)
	for i := 0; i < len(queue); i++ {
		usage := queue[i]
		o, err := origin.Parse(usage.resource)
		if err != nil {
			return nil, err
		}
		e := lockedEntry(l, o)
		if e == nil {
			return nil, fmt.Errorf("%s: missing lock entry for %s", root, usage.resource)
		}
		if dep := packages[e.Resource]; dep != nil {
			usage.from.direct = append(usage.from.direct, dep)
			continue
		}
		dep := &ModelPackage{Resource: e.Resource}
		packages[e.Resource] = dep
		usage.from.direct = append(usage.from.direct, dep)
		for _, u := range e.Usage {
			queue = append(queue, edge{dep, u.Resource})
		}
		dir := libPath(filepath.Join(root, project.LibDir), e)
		destination := filepath.Join(scratch, fmt.Sprintf("dependency-%d", i))
		if e.Kind == lock.KindStandard {
			continue
		}
		if e.Kind == lock.KindLocal {
			dir = e.Source
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(root, filepath.FromSlash(dir))
			}
		}
		var models []string
		if e.Kind == lock.KindArchive {
			models, err = extractLockedText(dir, destination, e.SHA256)
		} else {
			models, err = copyTextModels(dir, destination)
		}
		if err != nil {
			return nil, err
		}
		dep.Files = models
	}
	return model, nil
}

func extractLockedText(file, dst, sha256 string) ([]string, error) {
	a, err := project.OpenArchive(file)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	if a.SHA256 != sha256 {
		return nil, fmt.Errorf("%s: archive no longer matches the lock", file)
	}
	return a.ExtractTextModels(dst)
}

func copyTextModels(src, dst string) ([]string, error) {
	members, err := project.Members(src)
	if err != nil {
		return nil, err
	}
	files, err := project.TextModelMembers(members)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src, err)
	}
	for i, member := range files {
		name := filepath.FromSlash(member)
		data, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			return nil, err
		}
		target := filepath.Join(dst, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return nil, err
		}
		files[i] = target
	}
	return files, nil
}
