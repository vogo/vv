package skillevolve

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	skillDirPerm  = 0o700
	skillFilePerm = 0o600
)

type skillFrontmatter struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	License     string            `yaml:"license,omitempty"`
	Metadata    map[string]string `yaml:"metadata,omitempty"`
}

// WriteSkillMD writes <skillDir>/<name>/SKILL.md using exclusive directory
// create (os.Mkdir, not exist-ok). If the skill directory already exists it
// returns ErrSkillExists and does not RemoveAll. created is true only when
// this call created the directory; callers must RemoveAll only then.
func WriteSkillMD(skillDir string, s ExtractedSkill) (basePath string, created bool, err error) {
	if strings.TrimSpace(skillDir) == "" {
		return "", false, fmt.Errorf("skill dir is empty")
	}
	if strings.TrimSpace(s.Name) == "" {
		return "", false, fmt.Errorf("skill name is empty")
	}
	if err := os.MkdirAll(skillDir, skillDirPerm); err != nil {
		return "", false, err
	}
	basePath = filepath.Join(skillDir, s.Name)
	skillFile := filepath.Join(basePath, "SKILL.md")

	if err := os.Mkdir(basePath, skillDirPerm); err != nil {
		if os.IsExist(err) {
			return "", false, &ErrSkillExists{Name: s.Name}
		}
		return "", false, err
	}

	body := renderSkillMarkdown(s)
	f, err := os.OpenFile(skillFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, skillFilePerm)
	if err != nil {
		_ = os.RemoveAll(basePath)
		if os.IsExist(err) {
			return "", false, &ErrSkillExists{Name: s.Name}
		}
		return "", false, err
	}
	if _, err := f.Write([]byte(body)); err != nil {
		_ = f.Close()
		_ = os.RemoveAll(basePath)
		return "", false, err
	}
	if err := f.Close(); err != nil {
		_ = os.RemoveAll(basePath)
		return "", false, err
	}
	return basePath, true, nil
}

func renderSkillMarkdown(s ExtractedSkill) string {
	fm := skillFrontmatter{
		Name:        s.Name,
		Description: s.Description,
		License:     "UNLICENSED",
		Metadata:    s.Metadata,
	}
	raw, err := yaml.Marshal(fm)
	if err != nil {
		raw = []byte("name: " + s.Name + "\n")
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(raw)
	if !strings.HasSuffix(string(raw), "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("---\n\n")
	b.WriteString(s.Instructions)
	if s.Instructions != "" && !strings.HasSuffix(s.Instructions, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}
