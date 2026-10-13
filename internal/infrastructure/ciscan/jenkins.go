package ciscan

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/bakhod1r/env4ci/internal/domain"
)

// Jenkins credentials are what a Jenkinsfile reads from Jenkins storage:
// credentials('ID'), credentialsId: 'ID' (withCredentials, git, docker ...)
// and sshagent(['ID']). IDs that are not valid env keys are skipped.
var (
	jkCredentials = regexp.MustCompile(`\bcredentials\s*\(\s*['"]([^'"]+)['"]\s*\)`)
	jkCredsID     = regexp.MustCompile(`\bcredentialsId\s*:\s*['"]([^'"]+)['"]`)
	jkSSHAgent    = regexp.MustCompile(`\bsshagent\s*\(\s*(?:credentials\s*:\s*)?\[([^\]]*)\]`)
	jkQuoted      = regexp.MustCompile(`['"]([^'"]+)['"]`)
)

func scanJenkinsDir(root string) ([]domain.Reference, error) {
	var paths []string
	for _, pat := range []string{"Jenkinsfile", "Jenkinsfile.*", "*.Jenkinsfile", "*.jenkinsfile"} {
		m, _ := filepath.Glob(filepath.Join(root, pat))
		paths = append(paths, m...)
	}
	sort.Strings(paths)
	var refs []domain.Reference
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(root, p)
		refs = append(refs, scanJenkins(string(b), filepath.ToSlash(rel))...)
	}
	return refs, nil
}

func scanJenkins(src, rel string) []domain.Reference {
	var ids []string
	for _, re := range []*regexp.Regexp{jkCredentials, jkCredsID} {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			ids = append(ids, m[1])
		}
	}
	for _, m := range jkSSHAgent.FindAllStringSubmatch(src, -1) {
		for _, q := range jkQuoted.FindAllStringSubmatch(m[1], -1) {
			ids = append(ids, q[1])
		}
	}
	var refs []domain.Reference
	for _, id := range ids {
		if domain.ValidateKey(id) != nil {
			continue
		}
		refs = append(refs, domain.Reference{Key: id, Kind: domain.KindSecret, Provider: "jenkins",
			Stages: []string{StageAll}, Branches: []string{domain.BranchAll}, Sources: []string{rel}})
	}
	return refs
}
