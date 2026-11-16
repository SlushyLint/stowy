package extract

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type installTransaction struct {
	root        string
	workDir     string
	changed     []string
	backups     map[string]string
	directories []string
}

func commitPackage(home, root, workDir, pkg, version string, dependencies []string, stagedRoot string, files, dirs, regularFiles []string) error {
	current, err := readManifest(manifestPath(home, pkg))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read previous package manifest: %w", err)
	}
	currentFiles := make(map[string]struct{}, len(current.Files))
	for _, file := range current.Files {
		currentFiles[file] = struct{}{}
	}
	otherPaths, err := pathsOwnedByOtherPackages(home, pkg)
	if err != nil {
		return fmt.Errorf("inspect other package manifests: %w", err)
	}
	if err := preflightPackage(root, stagedRoot, pkg, files, dirs, otherPaths, currentFiles); err != nil {
		return err
	}
	commands, err := preflightCommands(home, root, stagedRoot, files)
	if err != nil {
		return err
	}

	tx := &installTransaction{
		root:    root,
		workDir: workDir,
		backups: make(map[string]string),
	}
	committedFiles := make(map[string]struct{}, len(files))
	rollback := func(cause error) error {
		if err := tx.rollback(); err != nil {
			return fmt.Errorf("%w (rollback failed: %v)", cause, err)
		}
		return cause
	}

	if err := tx.ensureRoot(); err != nil {
		return rollback(err)
	}
	for _, dir := range sortedDirectories(dirs, files) {
		if err := tx.ensureDirectory(dir); err != nil {
			return rollback(err)
		}
	}

	sortedFiles := append([]string(nil), files...)
	sort.Strings(sortedFiles)
	for _, file := range sortedFiles {
		target := filepath.Join(root, file)
		if _, shared := otherPaths[file]; shared {
			equal, err := sameEntry(filepath.Join(stagedRoot, file), target)
			if err != nil || !equal {
				if err != nil {
					return rollback(fmt.Errorf("compare shared file %q: %w", file, err))
				}
				return rollback(fmt.Errorf("file %q conflicts with another installed package", file))
			}
			continue
		}
		backedUp := false
		if _, owned := currentFiles[file]; owned {
			if _, err := os.Lstat(target); err == nil {
				if err := tx.backupPath(target, filepath.Join("root", file)); err != nil {
					return rollback(fmt.Errorf("backup existing file %q: %w", file, err))
				}
				backedUp = true
			} else if !os.IsNotExist(err) {
				return rollback(fmt.Errorf("inspect existing file %q: %w", file, err))
			}
		}
		if !backedUp {
			tx.markChanged(target)
		}
		if err := os.Rename(filepath.Join(stagedRoot, file), target); err != nil {
			return rollback(fmt.Errorf("install file %q: %w", file, err))
		}
		committedFiles[file] = struct{}{}
	}

	newFiles := make(map[string]struct{}, len(files))
	for _, file := range files {
		newFiles[file] = struct{}{}
	}
	for _, file := range current.Files {
		if _, stillInstalled := newFiles[file]; stillInstalled {
			continue
		}
		if _, shared := otherPaths[file]; shared {
			continue
		}
		target := filepath.Join(root, file)
		if _, err := os.Lstat(target); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return rollback(fmt.Errorf("inspect stale package file %q: %w", file, err))
		}
		if err := tx.backupPath(target, filepath.Join("stale", file)); err != nil {
			return rollback(fmt.Errorf("remove stale package file %q: %w", file, err))
		}
	}

	for _, file := range regularFiles {
		if _, committed := committedFiles[file]; !committed {
			continue
		}
		rewritten, err := rewriteAbsoluteELFNeededPaths(filepath.Join(root, file), root)
		if err != nil {
			return rollback(fmt.Errorf("normalize ELF dependencies in %q: %w", file, err))
		}
		if rewritten > 0 {
			logStep("relocate", "rewrote %d absolute ELF dependency path(s) in %s", rewritten, file)
		}
	}

	for _, command := range commands {
		if _, err := os.Lstat(command); err == nil {
			if err := tx.backupPath(command, filepath.Join("commands", filepath.Base(command))); err != nil {
				return rollback(fmt.Errorf("backup command launcher %q: %w", command, err))
			}
		} else if !os.IsNotExist(err) {
			return rollback(fmt.Errorf("inspect command launcher %q: %w", command, err))
		}
		if _, backedUp := tx.backups[command]; !backedUp {
			tx.markChanged(command)
		}
	}
	if err := expose(home, root, files); err != nil {
		return rollback(fmt.Errorf("expose package commands: %w", err))
	}
	if err := writeManifest(home, pkg, version, dependencies, files, dirs); err != nil {
		return rollback(fmt.Errorf("write package manifest: %w", err))
	}
	return nil
}

func preflightPackage(root, stagedRoot, pkg string, files, dirs []string, otherPaths, currentFiles map[string]struct{}) error {
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if _, duplicate := seen[file]; duplicate {
			return fmt.Errorf("package archive contains duplicate path %q", file)
		}
		seen[file] = struct{}{}
		target := filepath.Join(root, file)
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			if _, shared := otherPaths[file]; shared {
				return fmt.Errorf("package file %q is recorded as owned by another package but is missing", file)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect existing file %q: %w", file, err)
		}
		if _, shared := otherPaths[file]; shared {
			equal, err := sameEntry(filepath.Join(stagedRoot, file), target)
			if err != nil {
				return fmt.Errorf("compare shared file %q: %w", file, err)
			}
			if !equal {
				return fmt.Errorf("file %q conflicts with another installed package", file)
			}
			continue
		}
		if _, owned := currentFiles[file]; !owned {
			return fmt.Errorf("file %q already exists and is not owned by package %q", file, pkg)
		}
		if info.IsDir() {
			return fmt.Errorf("package file %q conflicts with an existing directory", file)
		}
	}
	for _, dir := range dirs {
		target := filepath.Join(root, dir)
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect existing directory %q: %w", dir, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("package directory %q conflicts with an existing non-directory", dir)
		}
	}
	return nil
}

func preflightCommands(home, root, stagedRoot string, files []string) ([]string, error) {
	var commands []string
	for _, file := range files {
		if filepath.Dir(file) != filepath.Join("usr", "bin") {
			continue
		}
		info, err := os.Stat(filepath.Join(stagedRoot, file))
		if err != nil {
			return nil, fmt.Errorf("inspect package command %q: %w", file, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		command := filepath.Join(home, ".local", "bin", filepath.Base(file))
		existing, err := os.Lstat(command)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect command path %q: %w", command, err)
		}
		if existing.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(command)
			if err != nil {
				return nil, fmt.Errorf("inspect command link %q: %w", command, err)
			}
			if target != filepath.Join(root, file) {
				return nil, fmt.Errorf("command %q already exists", command)
			}
		} else if existing.Mode().IsRegular() && existing.Size() <= 16*1024 {
			data, err := os.ReadFile(command)
			if err != nil {
				return nil, fmt.Errorf("read command path %q: %w", command, err)
			}
			if !isManagedCommandLauncher(filepath.Base(file), string(data)) {
				return nil, fmt.Errorf("command %q already exists", command)
			}
		} else {
			return nil, fmt.Errorf("command %q already exists", command)
		}
		commands = append(commands, command)
	}
	return commands, nil
}

func sameEntry(source, target string) (bool, error) {
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return false, err
	}
	targetInfo, err := os.Lstat(target)
	if err != nil {
		return false, err
	}
	if sourceInfo.Mode()&os.ModeSymlink != targetInfo.Mode()&os.ModeSymlink ||
		sourceInfo.Mode().Perm() != targetInfo.Mode().Perm() {
		return false, nil
	}
	if sourceInfo.Mode()&os.ModeSymlink != 0 {
		sourceTarget, err := os.Readlink(source)
		if err != nil {
			return false, err
		}
		targetTarget, err := os.Readlink(target)
		return sourceTarget == targetTarget, err
	}
	if !sourceInfo.Mode().IsRegular() || !targetInfo.Mode().IsRegular() || sourceInfo.Size() != targetInfo.Size() {
		return false, nil
	}
	sourceFile, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer sourceFile.Close()
	targetFile, err := os.Open(target)
	if err != nil {
		return false, err
	}
	defer targetFile.Close()
	sourceHash, targetHash := sha256.New(), sha256.New()
	if _, err := io.Copy(sourceHash, sourceFile); err != nil {
		return false, err
	}
	if _, err := io.Copy(targetHash, targetFile); err != nil {
		return false, err
	}
	return string(sourceHash.Sum(nil)) == string(targetHash.Sum(nil)), nil
}

func sortedDirectories(dirs, files []string) []string {
	seen := make(map[string]struct{})
	for _, dir := range dirs {
		seen[dir] = struct{}{}
	}
	for _, file := range files {
		dir := filepath.Dir(file)
		for dir != "." {
			seen[dir] = struct{}{}
			dir = filepath.Dir(dir)
		}
	}
	result := make([]string, 0, len(seen))
	for dir := range seen {
		result = append(result, dir)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := depth(result[i]), depth(result[j])
		if left != right {
			return left < right
		}
		return result[i] < result[j]
	})
	return result
}

func depth(path string) int {
	return strings.Count(filepath.Clean(path), string(filepath.Separator)) + 1
}

func (tx *installTransaction) ensureRoot() error {
	info, err := os.Lstat(tx.root)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("install root %q is not a real directory", tx.root)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err := os.Mkdir(tx.root, 0o755); err != nil {
		return err
	}
	tx.directories = append(tx.directories, tx.root)
	return nil
}

func (tx *installTransaction) ensureDirectory(rel string) error {
	if rel == "" || rel == "." || !filepath.IsLocal(rel) || filepath.Clean(rel) != rel {
		return fmt.Errorf("unsafe package directory %q", rel)
	}
	current := tx.root
	for _, part := range splitPath(rel) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("install path %q is not a real directory", current)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.Mkdir(current, 0o755); err != nil {
			return err
		}
		tx.directories = append(tx.directories, current)
	}
	return nil
}

func splitPath(path string) []string {
	var parts []string
	for path != "." && path != "" {
		dir, file := filepath.Split(path)
		parts = append([]string{file}, parts...)
		path = filepath.Clean(dir)
	}
	return parts
}

func (tx *installTransaction) markChanged(path string) {
	tx.changed = append(tx.changed, path)
}

func (tx *installTransaction) backupPath(path, relative string) error {
	backup := filepath.Join(tx.workDir, "backup", relative)
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		return err
	}
	if err := os.Rename(path, backup); err != nil {
		return err
	}
	tx.backups[path] = backup
	tx.changed = append(tx.changed, path)
	return nil
}

func (tx *installTransaction) rollback() error {
	var firstErr error
	for i := len(tx.changed) - 1; i >= 0; i-- {
		path := tx.changed[i]
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
		if backup := tx.backups[path]; backup != "" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if err := os.Rename(backup, path); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	for i := len(tx.directories) - 1; i >= 0; i-- {
		if err := os.Remove(tx.directories[i]); err != nil && !os.IsNotExist(err) && !os.IsExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
