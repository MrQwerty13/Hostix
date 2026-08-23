package image

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// dockerignoreMatcher implements the portable, commonly used subset of
// Docker's ignore syntax: ordered patterns, ! negation, *, ?, character
// classes, and ** when used as a complete path segment. Leading and trailing
// slashes are ignored, and a pattern without a slash matches a basename at any
// depth. Platform-specific escaping and Dockerfile-specific ignore files are
// intentionally outside this subset.
type dockerignoreMatcher struct {
	patterns     []dockerignorePattern
	hasNegations bool
}

type dockerignorePattern struct {
	segments     []string
	basenameOnly bool
	negated      bool
}

func loadDockerignore(projectDir string) (dockerignoreMatcher, error) {
	ignorePath := filepath.Join(projectDir, ".dockerignore")
	info, err := os.Lstat(ignorePath)
	if errors.Is(err, os.ErrNotExist) {
		return dockerignoreMatcher{}, nil
	}
	if err != nil {
		return dockerignoreMatcher{}, fmt.Errorf("inspect .dockerignore: %w", err)
	}
	if !info.Mode().IsRegular() {
		return dockerignoreMatcher{}, fmt.Errorf(".dockerignore must be a regular file")
	}

	content, err := os.ReadFile(ignorePath)
	if err != nil {
		return dockerignoreMatcher{}, fmt.Errorf("read .dockerignore: %w", err)
	}
	matcher, err := parseDockerignore(string(content))
	if err != nil {
		return dockerignoreMatcher{}, fmt.Errorf("parse .dockerignore: %w", err)
	}
	return matcher, nil
}

func parseDockerignore(content string) (dockerignoreMatcher, error) {
	var matcher dockerignoreMatcher
	for index, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if index == 0 {
			line = strings.TrimPrefix(line, "\ufeff")
		}
		if line == "" {
			continue
		}

		escapedPrefix := strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`)
		if escapedPrefix {
			line = line[1:]
		} else if strings.HasPrefix(line, "#") {
			continue
		}

		negated := false
		if !escapedPrefix && strings.HasPrefix(line, "!") {
			negated = true
			line = strings.TrimSpace(strings.TrimPrefix(line, "!"))
			if line == "" {
				return dockerignoreMatcher{}, fmt.Errorf("line %d: negation pattern is empty", index+1)
			}
		}

		line = strings.Trim(line, "/")
		if line == "" {
			continue
		}
		line = path.Clean(line)
		if line == "." {
			continue
		}
		if line == ".." || strings.HasPrefix(line, "../") {
			return dockerignoreMatcher{}, fmt.Errorf("line %d: pattern %q escapes the build context", index+1, rawLine)
		}

		segments := strings.Split(line, "/")
		for _, segment := range segments {
			if segment == "**" {
				continue
			}
			if _, err := path.Match(segment, ""); err != nil {
				return dockerignoreMatcher{}, fmt.Errorf("line %d: invalid pattern %q: %w", index+1, rawLine, err)
			}
		}

		matcher.patterns = append(matcher.patterns, dockerignorePattern{
			segments:     segments,
			basenameOnly: len(segments) == 1,
			negated:      negated,
		})
		matcher.hasNegations = matcher.hasNegations || negated
	}
	return matcher, nil
}

func (m dockerignoreMatcher) excludes(tarPath string) bool {
	if len(m.patterns) == 0 {
		return false
	}

	pathSegments := strings.Split(strings.Trim(tarPath, "/"), "/")
	excluded := false
	for _, pattern := range m.patterns {
		if pattern.matchesPathOrParent(pathSegments) {
			excluded = !pattern.negated
		}
	}
	return excluded
}

func (p dockerignorePattern) matchesPathOrParent(pathSegments []string) bool {
	for length := len(pathSegments); length > 0; length-- {
		candidate := pathSegments[:length]
		if p.basenameOnly {
			matched, _ := path.Match(p.segments[0], candidate[len(candidate)-1])
			if matched {
				return true
			}
			continue
		}
		if matchDockerignoreSegments(p.segments, candidate) {
			return true
		}
	}
	return false
}

func matchDockerignoreSegments(pattern, candidate []string) bool {
	if len(pattern) == 0 {
		return len(candidate) == 0
	}
	if pattern[0] == "**" {
		if matchDockerignoreSegments(pattern[1:], candidate) {
			return true
		}
		return len(candidate) > 0 && matchDockerignoreSegments(pattern, candidate[1:])
	}
	if len(candidate) == 0 {
		return false
	}
	matched, _ := path.Match(pattern[0], candidate[0])
	return matched && matchDockerignoreSegments(pattern[1:], candidate[1:])
}
