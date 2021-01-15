package buildpack

import (
	"bytes"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/BurntSushi/toml"
	"github.com/pkg/errors"

	"github.com/buildpacks/lifecycle/api"
	lerrors "github.com/buildpacks/lifecycle/errors"
)

const EnvBuildpackDir = "CNB_BUILDPACK_DIR"

var (
	errFailedDetection = errors.New("no buildpacks participating")
	errBuildpack       = errors.New("buildpack(s) failed with err")
)

type Logger interface {
	Debug(msg string)
	Debugf(fmt string, v ...interface{})

	Info(msg string)
	Infof(fmt string, v ...interface{})

	Warn(msg string)
	Warnf(fmt string, v ...interface{})

	Error(msg string)
	Errorf(fmt string, v ...interface{})
}

type Processor interface {
	Process(done []GroupBuildpack) ([]GroupBuildpack, []BuildPlanEntry, error)
	Runs() *sync.Map
	SetRuns(runs *sync.Map)
}

type DetectConfig struct {
	FullEnv       []string
	ClearEnv      []string
	AppDir        string
	PlatformDir   string
	BuildpacksDir string
	Logger        Logger
}

func (bo BuildpackOrder) Detect(config *DetectConfig, processor Processor) (BuildpackGroup, BuildPlan, error) {
	if processor.Runs() == nil {
		processor.SetRuns(&sync.Map{})
	}
	bps, entries, err := bo.detect(config, processor, nil, nil, false, &sync.WaitGroup{})
	if err != nil && err.Error() == errBuildpack.Error() {
		err = lerrors.NewLifecycleError(err, lerrors.ErrTypeBuildpack)
	} else if err != nil && err.Error() == errFailedDetection.Error() {
		err = lerrors.NewLifecycleError(err, lerrors.ErrTypeFailedDetection)
	}
	for i := range entries {
		for j := range entries[i].Requires {
			entries[i].Requires[j].convertVersionToMetadata()
		}
	}
	return BuildpackGroup{Group: bps}, BuildPlan{Entries: entries}, err
}

func (bo BuildpackOrder) detect(config *DetectConfig, processor Processor, done, next []GroupBuildpack, optional bool, wg *sync.WaitGroup) ([]GroupBuildpack, []BuildPlanEntry, error) {
	ngroup := BuildpackGroup{Group: next}
	buildpackErr := false
	for _, group := range bo {
		// FIXME: double-check slice safety here
		found, plan, err := group.append(ngroup).detect(config, processor, done, wg)
		if err == errBuildpack {
			buildpackErr = true
		}
		if err == errFailedDetection || err == errBuildpack {
			wg = &sync.WaitGroup{}
			continue
		}
		return found, plan, err
	}
	if optional {
		return ngroup.detect(config, processor, done, wg)
	}

	if buildpackErr {
		return nil, nil, errBuildpack
	}
	return nil, nil, errFailedDetection
}

func (bg BuildpackGroup) Detect(config *DetectConfig, processor Processor) (BuildpackGroup, BuildPlan, error) {
	if processor.Runs() == nil {
		processor.SetRuns(&sync.Map{})
	}
	bps, entries, err := bg.detect(config, processor, nil, &sync.WaitGroup{})
	if err != nil && err.Error() == errBuildpack.Error() {
		err = lerrors.NewLifecycleError(err, lerrors.ErrTypeBuildpack)
	} else if err != nil && err.Error() == errFailedDetection.Error() {
		err = lerrors.NewLifecycleError(err, lerrors.ErrTypeFailedDetection)
	}
	for i := range entries {
		for j := range entries[i].Requires {
			entries[i].Requires[j].convertVersionToMetadata()
		}
	}
	return BuildpackGroup{Group: bps}, BuildPlan{Entries: entries}, err
}

func (bg BuildpackGroup) detect(config *DetectConfig, processor Processor, done []GroupBuildpack, wg *sync.WaitGroup) ([]GroupBuildpack, []BuildPlanEntry, error) {
	for i, bp := range bg.Group {
		key := bp.String()
		if hasID(done, bp.ID) {
			continue
		}
		info, err := bp.Lookup(config.BuildpacksDir)
		if err != nil {
			return nil, nil, err
		}
		bp.API = info.API
		bp.Homepage = info.Buildpack.Homepage
		if info.Order != nil {
			// TODO: double-check slice safety here
			// FIXME: cyclical references lead to infinite recursion
			return info.Order.detect(config, processor, done, bg.Group[i+1:], bp.Optional, wg)
		}
		done = append(done, bp)
		wg.Add(1)
		go func() {
			if _, ok := processor.Runs().Load(key); !ok {
				processor.Runs().Store(key, info.Detect(config))
			}
			wg.Done()
		}()
	}

	wg.Wait()

	return processor.Process(done)
}

func (bg BuildpackGroup) append(group ...BuildpackGroup) BuildpackGroup {
	for _, g := range group {
		bg.Group = append(bg.Group, g.Group...)
	}
	return bg
}

func hasID(bps []GroupBuildpack, id string) bool {
	for _, bp := range bps {
		if bp.ID == id {
			return true
		}
	}
	return false
}

func (b *BuildpackTOML) Detect(config *DetectConfig) DetectRun { // TODO: config should be a pointer
	appDir, err := filepath.Abs(config.AppDir)
	if err != nil {
		return DetectRun{Code: -1, Err: err}
	}
	platformDir, err := filepath.Abs(config.PlatformDir)
	if err != nil {
		return DetectRun{Code: -1, Err: err}
	}
	planDir, err := ioutil.TempDir("", "plan.")
	if err != nil {
		return DetectRun{Code: -1, Err: err}
	}
	defer os.RemoveAll(planDir)

	planPath := filepath.Join(planDir, "plan.toml")
	if err := ioutil.WriteFile(planPath, nil, 0777); err != nil {
		return DetectRun{Code: -1, Err: err}
	}

	out := &bytes.Buffer{}
	cmd := exec.Command(
		filepath.Join(b.Dir, "bin", "detect"),
		platformDir,
		planPath,
	)
	cmd.Dir = appDir
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Env = config.FullEnv
	if b.Buildpack.ClearEnv {
		cmd.Env = config.ClearEnv
	}
	cmd.Env = append(cmd.Env, EnvBuildpackDir+"="+b.Dir)

	if err := cmd.Run(); err != nil {
		if err, ok := err.(*exec.ExitError); ok {
			if status, ok := err.Sys().(syscall.WaitStatus); ok {
				return DetectRun{Code: status.ExitStatus(), Output: out.Bytes()}
			}
		}
		return DetectRun{Code: -1, Err: err, Output: out.Bytes()}
	}
	var t DetectRun
	if _, err := toml.DecodeFile(planPath, &t); err != nil {
		return DetectRun{Code: -1, Err: err}
	}
	if api.MustParse(b.API).Equal(api.MustParse("0.2")) {
		if t.hasInconsistentVersions() || t.Or.hasInconsistentVersions() {
			t.Err = errors.Errorf(`buildpack %s has a "version" key that does not match "metadata.version"`, b.Buildpack.ID)
			t.Code = -1
		}
	}
	if api.MustParse(b.API).Compare(api.MustParse("0.3")) >= 0 {
		if t.hasDoublySpecifiedVersions() || t.Or.hasDoublySpecifiedVersions() {
			t.Err = errors.Errorf(`buildpack %s has a "version" key and a "metadata.version" which cannot be specified together. "metadata.version" should be used instead`, b.Buildpack.ID)
			t.Code = -1
		}
	}
	if api.MustParse(b.API).Compare(api.MustParse("0.3")) >= 0 {
		if t.hasTopLevelVersions() || t.Or.hasTopLevelVersions() {
			config.Logger.Warnf(`Warning: buildpack %s has a "version" key. This key is deprecated in build plan requirements in buildpack API 0.3. "metadata.version" should be used instead`, b.Buildpack.ID)
		}
	}
	t.Output = out.Bytes()
	return t
}

type BuildPlan struct {
	Entries []BuildPlanEntry `toml:"entries"`
}

func (p BuildPlan) Find(bpID string) BuildpackPlan {
	var out []Require
	for _, entry := range p.Entries {
		for _, provider := range entry.Providers {
			if provider.ID == bpID {
				out = append(out, entry.Requires...)
				break
			}
		}
	}
	return BuildpackPlan{Entries: out}
}

// TODO: ensure at least one claimed entry of each name is provided by the BP
func (p BuildPlan) Filter(metRequires []string) BuildPlan {
	var out []BuildPlanEntry
	for _, planEntry := range p.Entries {
		if !containsEntry(metRequires, planEntry) {
			out = append(out, planEntry)
		}
	}
	return BuildPlan{Entries: out}
}

func containsEntry(metRequires []string, entry BuildPlanEntry) bool {
	for _, met := range metRequires {
		for _, planReq := range entry.Requires {
			if met == planReq.Name {
				return true
			}
		}
	}
	return false
}

type BuildPlanEntry struct {
	Providers []GroupBuildpack `toml:"providers"`
	Requires  []Require        `toml:"requires"`
}

func (be BuildPlanEntry) NoOpt() BuildPlanEntry {
	var out []GroupBuildpack
	for _, p := range be.Providers {
		out = append(out, p.NoOpt().NoAPI().NoHomepage())
	}
	be.Providers = out
	return be
}

type Provide struct {
	Name string `toml:"name"`
}

type DetectRun struct {
	PlanSections
	Or     planSectionsList `toml:"or"`
	Output []byte           `toml:"-"`
	Code   int              `toml:"-"`
	Err    error            `toml:"-"`
}

type PlanSections struct {
	Requires []Require `toml:"requires"`
	Provides []Provide `toml:"provides"`
}

func (p *PlanSections) hasInconsistentVersions() bool {
	for _, req := range p.Requires {
		if req.hasInconsistentVersions() {
			return true
		}
	}
	return false
}

func (p *PlanSections) hasDoublySpecifiedVersions() bool {
	for _, req := range p.Requires {
		if req.hasDoublySpecifiedVersions() {
			return true
		}
	}
	return false
}

func (p *PlanSections) hasTopLevelVersions() bool {
	for _, req := range p.Requires {
		if req.hasTopLevelVersions() {
			return true
		}
	}
	return false
}

type planSectionsList []PlanSections

func (p *planSectionsList) hasInconsistentVersions() bool {
	for _, planSection := range *p {
		if planSection.hasInconsistentVersions() {
			return true
		}
	}
	return false
}

func (p *planSectionsList) hasDoublySpecifiedVersions() bool {
	for _, planSection := range *p {
		if planSection.hasDoublySpecifiedVersions() {
			return true
		}
	}
	return false
}

func (p *planSectionsList) hasTopLevelVersions() bool {
	for _, planSection := range *p {
		if planSection.hasTopLevelVersions() {
			return true
		}
	}
	return false
}
