package buildpack_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lerrors "github.com/buildpacks/lifecycle/errors"

	"github.com/BurntSushi/toml"
	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/sclevine/spec"
	"github.com/sclevine/spec/report"

	"github.com/buildpacks/lifecycle/api"
	"github.com/buildpacks/lifecycle/buildpack"
	"github.com/buildpacks/lifecycle/env"
	"github.com/buildpacks/lifecycle/launch"
	"github.com/buildpacks/lifecycle/layers"
	h "github.com/buildpacks/lifecycle/testhelpers"
	"github.com/buildpacks/lifecycle/testmock"
)

// TODO: fix duplicated test fixtures

var latestBuildpackAPI = api.Buildpack.Latest()

func TestBuildpackTOML(t *testing.T) {
	spec.Run(t, "BuildpackTOML", testBuildpackTOML, spec.Report(report.Terminal{}))
}

//go:generate mockgen -package testmock -destination testmock/env.go github.com/buildpacks/lifecycle BuildEnv

func testBuildpackTOML(t *testing.T, when spec.G, it spec.S) {
	var (
		bpTOML         buildpack.BuildpackTOML
		mockCtrl       *gomock.Controller
		mockEnv        *testmock.MockBuildEnv
		stdout, stderr *bytes.Buffer
		tmpDir         string
		platformDir    string
		appDir         string
		layersDir      string
		buildpacksDir  string
		config         buildpack.BuildConfig
	)

	it.Before(func() {
		mockCtrl = gomock.NewController(t)
		mockEnv = testmock.NewMockBuildEnv(mockCtrl)

		var err error
		tmpDir, err = ioutil.TempDir("", "lifecycle")
		if err != nil {
			t.Fatalf("Error: %s\n", err)
		}
		stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
		platformDir = filepath.Join(tmpDir, "platform")
		layersDir = filepath.Join(tmpDir, "launch")
		appDir = filepath.Join(layersDir, "app")
		h.Mkdir(t, layersDir, appDir, filepath.Join(platformDir, "env"))

		buildpacksDir, err = filepath.Abs(filepath.Join("testdata", "by-id"))
		if err != nil {
			t.Fatalf("Error: %s\n", err)
		}

		config = buildpack.BuildConfig{
			Env:         mockEnv,
			AppDir:      appDir,
			PlatformDir: platformDir,
			LayersDir:   layersDir,
			Out:         stdout,
			Err:         stderr,
		}

		bpTOML = buildpack.BuildpackTOML{
			API: latestBuildpackAPI.String(),
			Buildpack: buildpack.BuildpackInfo{
				ID:       "A",
				Version:  "v1",
				Name:     "Buildpack A",
				ClearEnv: false,
				Homepage: "Buildpack A Homepage",
			},
			Dir: filepath.Join(buildpacksDir, "A", "v1"),
		}
	})

	it.After(func() {
		os.RemoveAll(tmpDir)
		mockCtrl.Finish()
	})

	when("#Build", func() {
		when("building succeeds", func() {
			it.Before(func() {
				mockEnv.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
			})

			it("should ensure the buildpack's layers dir exists and process build layers", func() {
				h.Mkdir(t,
					filepath.Join(layersDir, "A"),
					filepath.Join(appDir, "layers-A-v1", "layer1"),
					filepath.Join(appDir, "layers-A-v1", "layer2"),
					filepath.Join(appDir, "layers-A-v1", "layer3"),
				)
				h.Mkfile(t, "build = true",
					filepath.Join(appDir, "layers-A-v1", "layer1.toml"),
					filepath.Join(appDir, "layers-A-v1", "layer3.toml"),
				)
				gomock.InOrder(
					mockEnv.EXPECT().AddRootDir(filepath.Join(layersDir, "A", "layer1")),
					mockEnv.EXPECT().AddRootDir(filepath.Join(layersDir, "A", "layer3")),
					mockEnv.EXPECT().AddEnvDir(filepath.Join(layersDir, "A", "layer1", "env"), env.ActionTypeOverride),
					mockEnv.EXPECT().AddEnvDir(filepath.Join(layersDir, "A", "layer1", "env.build"), env.ActionTypeOverride),
					mockEnv.EXPECT().AddEnvDir(filepath.Join(layersDir, "A", "layer3", "env"), env.ActionTypeOverride),
					mockEnv.EXPECT().AddEnvDir(filepath.Join(layersDir, "A", "layer3", "env.build"), env.ActionTypeOverride),
				)
				if _, err := bpTOML.Build(buildpack.BuildpackPlan{}, config); err != nil {
					t.Fatalf("Unexpected error:\n%s\n", err)
				}
				testExists(t,
					filepath.Join(layersDir, "A"),
				)
			})

			it("should provide the platform dir", func() {
				h.Mkfile(t, "some-data",
					filepath.Join(platformDir, "env", "SOME_VAR"),
				)
				if _, err := bpTOML.Build(buildpack.BuildpackPlan{}, config); err != nil {
					t.Fatalf("Unexpected error:\n%s\n", err)
				}
				testExists(t,
					filepath.Join(appDir, "build-env-A-v1", "SOME_VAR"),
				)
			})

			it("should provide environment variables", func() {
				if _, err := bpTOML.Build(buildpack.BuildpackPlan{}, config); err != nil {
					t.Fatalf("Unexpected error:\n%s\n", err)
				}
				if s := cmp.Diff(h.Rdfile(t, filepath.Join(appDir, "build-info-A-v1")),
					"TEST_ENV: Av1\n",
				); s != "" {
					t.Fatalf("Unexpected info:\n%s\n", s)
				}
			})

			it("should set CNB_BUILDPACK_DIR", func() {
				if _, err := bpTOML.Build(buildpack.BuildpackPlan{}, config); err != nil {
					t.Fatalf("Unexpected error:\n%s\n", err)
				}
				if s := cmp.Diff(h.Rdfile(t, filepath.Join(appDir, "build-env-cnb-buildpack-dir-A-v1")),
					filepath.Join(bpTOML.Dir),
				); s != "" {
					t.Fatalf("Unexpected CNB_BUILDPACK_DIR:\n%s\n", s)
				}
			})

			it("should connect stdout and stdin to the terminal", func() {
				if _, err := bpTOML.Build(buildpack.BuildpackPlan{}, config); err != nil {
					t.Fatalf("Unexpected error:\n%s\n", err)
				}
				if s := cmp.Diff(h.CleanEndings(stdout.String()), "build out: A@v1\n"); s != "" {
					t.Fatalf("Unexpected stdout:\n%s\n", s)
				}
				if s := cmp.Diff(h.CleanEndings(stderr.String()), "build err: A@v1\n"); s != "" {
					t.Fatalf("Unexpected stderr:\n%s\n", s)
				}
			})

			when("build result", func() {
				it("should get bom entries from launch.toml and unmet requires from build.toml", func() {
					bpPlan := buildpack.BuildpackPlan{
						Entries: []buildpack.Require{
							{
								Name:    "some-deprecated-bp-replace-version-dep",
								Version: "some-version-orig", // top-level version is deprecated in buildpack API 0.3
							},
							{
								Name:     "some-dep",
								Metadata: map[string]interface{}{"version": "v1"},
							},
							{
								Name:     "some-replace-version-dep",
								Metadata: map[string]interface{}{"version": "some-version-orig"},
							},
							{
								Name: "some-unmet-dep",
							},
						},
					}

					h.Mkfile(t,
						"[[bom]]\n"+
							`name = "some-deprecated-bp-replace-version-dep"`+"\n"+
							"[bom.metadata]\n"+
							`version = "some-version-new"`+"\n"+
							"[[bom]]\n"+
							`name = "some-dep"`+"\n"+
							"[bom.metadata]\n"+
							`version = "v1"`+"\n"+
							"[[bom]]\n"+
							`name = "some-replace-version-dep"`+"\n"+
							"[bom.metadata]\n"+
							`version = "some-version-new"`+"\n",
						filepath.Join(appDir, "launch-A-v1.toml"),
					)

					h.Mkfile(t,
						"[[unmet]]\n"+
							`name = "some-unmet-dep"`+"\n",
						filepath.Join(appDir, "build-A-v1.toml"),
					)

					br, err := bpTOML.Build(bpPlan, config)
					if err != nil {
						t.Fatalf("Unexpected error:\n%s\n", err)
					}

					if s := cmp.Diff(br, buildpack.BuildResult{
						BOM: []buildpack.BOMEntry{
							{
								Require: buildpack.Require{
									Name:     "some-deprecated-bp-replace-version-dep",
									Metadata: map[string]interface{}{"version": "some-version-new"},
								},
								Buildpack: buildpack.GroupBuildpack{ID: "A", Version: "v1"}, // no api, no homepage
							},
							{
								Require: buildpack.Require{
									Name:     "some-dep",
									Metadata: map[string]interface{}{"version": "v1"},
								},
								Buildpack: buildpack.GroupBuildpack{ID: "A", Version: "v1"}, // no api, no homepage
							},
							{
								Require: buildpack.Require{
									Name:     "some-replace-version-dep",
									Metadata: map[string]interface{}{"version": "some-version-new"},
								},
								Buildpack: buildpack.GroupBuildpack{ID: "A", Version: "v1"}, // no api, no homepage
							},
						},
						Labels:      []buildpack.Label{},
						MetRequires: []string{"some-deprecated-bp-replace-version-dep", "some-dep", "some-replace-version-dep"},
						Processes:   []launch.Process{},
						Slices:      []layers.Slice{},
					}); s != "" {
						t.Fatalf("Unexpected:\n%s\n", s)
					}
				})

				it("should include labels", func() {
					h.Mkfile(t,
						"[[labels]]\n"+
							`key = "some-key"`+"\n"+
							`value = "some-value"`+"\n"+
							"[[labels]]\n"+
							`key = "some-other-key"`+"\n"+
							`value = "some-other-value"`+"\n",
						filepath.Join(appDir, "launch-A-v1.toml"),
					)

					br, err := bpTOML.Build(buildpack.BuildpackPlan{}, config)
					if err != nil {
						t.Fatalf("Unexpected error:\n%s\n", err)
					}

					if s := cmp.Diff(br, buildpack.BuildResult{
						BOM: nil,
						Labels: []buildpack.Label{
							{Key: "some-key", Value: "some-value"},
							{Key: "some-other-key", Value: "some-other-value"},
						},
						MetRequires: nil,
						Processes:   []launch.Process{},
						Slices:      []layers.Slice{},
					}); s != "" {
						t.Fatalf("Unexpected:\n%s\n", s)
					}
				})

				it("should include processes", func() {
					h.Mkfile(t,
						`[[processes]]`+"\n"+
							`type = "some-type"`+"\n"+
							`command = "some-cmd"`+"\n"+
							`[[processes]]`+"\n"+
							`type = "other-type"`+"\n"+
							`command = "other-cmd"`+"\n",
						filepath.Join(appDir, "launch-A-v1.toml"),
					)
					br, err := bpTOML.Build(buildpack.BuildpackPlan{}, config)
					if err != nil {
						t.Fatalf("Unexpected error:\n%s\n", err)
					}
					if s := cmp.Diff(br, buildpack.BuildResult{
						BOM:         nil,
						Labels:      []buildpack.Label{},
						MetRequires: nil,
						Processes: []launch.Process{
							{Type: "some-type", Command: "some-cmd", BuildpackID: "A"},
							{Type: "other-type", Command: "other-cmd", BuildpackID: "A"},
						},
						Slices: []layers.Slice{},
					}); s != "" {
						t.Fatalf("Unexpected metadata:\n%s\n", s)
					}
				})

				it("should include slices", func() {
					h.Mkfile(t,
						"[[slices]]\n"+
							`paths = ["some-path", "some-other-path"]`+"\n",
						filepath.Join(appDir, "launch-A-v1.toml"),
					)

					br, err := bpTOML.Build(buildpack.BuildpackPlan{}, config)
					if err != nil {
						t.Fatalf("Unexpected error:\n%s\n", err)
					}

					if s := cmp.Diff(br, buildpack.BuildResult{
						BOM:         nil,
						Labels:      []buildpack.Label{},
						MetRequires: nil,
						Processes:   []launch.Process{},
						Slices:      []layers.Slice{{Paths: []string{"some-path", "some-other-path"}}},
					}); s != "" {
						t.Fatalf("Unexpected:\n%s\n", s)
					}
				})
			})
		})

		when("building succeeds with a clear env", func() {
			it.Before(func() {
				mockEnv.EXPECT().List().Return(append(os.Environ(), "TEST_ENV=cleared"))

				bpTOML.Buildpack.Version = "v1.clear"
				bpTOML.Dir = filepath.Join(buildpacksDir, "A", "v1.clear")
				bpTOML.Buildpack.ClearEnv = true
			})

			it("should not apply user-provided env vars", func() {
				if _, err := bpTOML.Build(buildpack.BuildpackPlan{}, config); err != nil {
					t.Fatalf("Error: %s\n", err)
				}
				if s := cmp.Diff(h.Rdfile(t, filepath.Join(appDir, "build-info-A-v1.clear")),
					"TEST_ENV: cleared\n",
				); s != "" {
					t.Fatalf("Unexpected info:\n%s\n", s)
				}
			})

			it("should set CNB_BUILDPACK_DIR", func() {
				if _, err := bpTOML.Build(buildpack.BuildpackPlan{}, config); err != nil {
					t.Fatalf("Error: %s\n", err)
				}
				if s := cmp.Diff(h.Rdfile(t, filepath.Join(appDir, "build-env-cnb-buildpack-dir-A-v1.clear")),
					bpTOML.Dir,
				); s != "" {
					t.Fatalf("Unexpected CNB_BUILDPACK_DIR:\n%s\n", s)
				}
			})
		})

		when("building fails", func() {
			it("should error when layer directories cannot be created", func() {
				h.Mkfile(t, "some-data", filepath.Join(layersDir, "A"))
				_, err := bpTOML.Build(buildpack.BuildpackPlan{}, config)
				if _, ok := err.(*os.PathError); !ok {
					t.Fatalf("Incorrect error: %s\n", err)
				}
			})

			it("should error when the provided buildpack plan is invalid", func() {
				bpPlan := buildpack.BuildpackPlan{
					Entries: []buildpack.Require{
						{
							Metadata: map[string]interface{}{"a": map[int64]int64{1: 2}}, // map with non-string key type
						},
					},
				}
				if _, err := bpTOML.Build(bpPlan, config); err == nil {
					t.Fatal("Expected error.\n")
				} else if !strings.Contains(err.Error(), "toml") {
					t.Fatalf("Incorrect error: %s\n", err)
				}
			})

			it("should error when the env cannot be found", func() {
				mockEnv.EXPECT().WithPlatform(platformDir).Return(nil, errors.New("some error"))
				if _, err := bpTOML.Build(buildpack.BuildpackPlan{}, config); err == nil {
					t.Fatal("Expected error.\n")
				} else if !strings.Contains(err.Error(), "some error") {
					t.Fatalf("Incorrect error: %s\n", err)
				}
			})

			it("should error when the command fails", func() {
				mockEnv.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
				if err := os.RemoveAll(platformDir); err != nil {
					t.Fatalf("Error: %s\n", err)
				}
				_, err := bpTOML.Build(buildpack.BuildpackPlan{}, config)
				if err, ok := err.(*lerrors.Error); !ok || err.Type != lerrors.ErrTypeBuildpack {
					t.Fatalf("Incorrect error: %s\n", err)
				}
			})

			when("modifying the env fails", func() {
				var appendErr error

				it.Before(func() {
					appendErr = errors.New("some error")
				})

				each(it, []func(){
					func() {
						mockEnv.EXPECT().AddRootDir(gomock.Any()).Return(appendErr)
					},
					func() {
						mockEnv.EXPECT().AddRootDir(gomock.Any()).Return(nil)
						mockEnv.EXPECT().AddRootDir(gomock.Any()).Return(appendErr)
					},
					func() {
						mockEnv.EXPECT().AddRootDir(gomock.Any()).Return(nil)
						mockEnv.EXPECT().AddRootDir(gomock.Any()).Return(nil)
						mockEnv.EXPECT().AddEnvDir(gomock.Any(), gomock.Any()).Return(appendErr)
					},
					func() {
						mockEnv.EXPECT().AddRootDir(gomock.Any()).Return(nil)
						mockEnv.EXPECT().AddRootDir(gomock.Any()).Return(nil)
						mockEnv.EXPECT().AddEnvDir(gomock.Any(), gomock.Any()).Return(nil)
						mockEnv.EXPECT().AddEnvDir(gomock.Any(), gomock.Any()).Return(appendErr)
					},
				}, "should error", func() {
					mockEnv.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
					h.Mkdir(t,
						filepath.Join(appDir, "layers-A-v1", "layer1"),
						filepath.Join(appDir, "layers-A-v1", "layer2"),
					)
					h.Mkfile(t, "build = true",
						filepath.Join(appDir, "layers-A-v1", "layer1.toml"),
						filepath.Join(appDir, "layers-A-v1", "layer2.toml"),
					)
					if _, err := bpTOML.Build(buildpack.BuildpackPlan{}, config); err != appendErr {
						t.Fatalf("Incorrect error: %s\n", err)
					}
				})
			})

			it("should error when launch.toml is not writable", func() {
				mockEnv.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
				h.Mkdir(t, filepath.Join(layersDir, "A", "launch.toml"))
				if _, err := bpTOML.Build(buildpack.BuildpackPlan{}, config); err == nil {
					t.Fatal("Expected error")
				}
			})

			it("should error when the launch bom has a top level version", func() {
				mockEnv.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
				h.Mkfile(t,
					"[[bom]]\n"+
						`name = "some-dep"`+"\n"+
						`version = "some-version"`+"\n",
					filepath.Join(appDir, "launch-A-v1.toml"),
				)
				_, err := bpTOML.Build(buildpack.BuildpackPlan{}, config)
				h.AssertNotNil(t, err)
				expected := "top level version which is not allowed"
				h.AssertStringContains(t, err.Error(), expected)
			})

			it("should error when the build bom has a top level version", func() {
				mockEnv.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
				h.Mkfile(t,
					"[[bom]]\n"+
						`name = "some-dep"`+"\n"+
						`version = "some-version"`+"\n",
					filepath.Join(appDir, "build-A-v1.toml"),
				)
				_, err := bpTOML.Build(buildpack.BuildpackPlan{}, config)
				h.AssertNotNil(t, err)
				expected := "top level version which is not allowed"
				h.AssertStringContains(t, err.Error(), expected)
			})

			when("invalid unmet entries", func() {
				when("missing name", func() {
					it("should error", func() {
						mockEnv.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
						h.Mkfile(t,
							"[[unmet]]\n",
							filepath.Join(appDir, "build-A-v1.toml"),
						)
						_, err := bpTOML.Build(buildpack.BuildpackPlan{}, config)
						h.AssertNotNil(t, err)
						expected := "name is required"
						h.AssertStringContains(t, err.Error(), expected)
					})
				})

				when("invalid name", func() {
					it("should error", func() {
						mockEnv.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
						h.Mkfile(t,
							"[[unmet]]\n"+
								`name = "unknown-dep"`+"\n",
							filepath.Join(appDir, "build-A-v1.toml"),
						)
						_, err := bpTOML.Build(buildpack.BuildpackPlan{}, config)
						h.AssertNotNil(t, err)
						expected := "must match a requested dependency"
						h.AssertStringContains(t, err.Error(), expected)
					})
				})
			})
		})

		when("buildpack api = 0.2", func() {
			it.Before(func() {
				bpTOML.API = "0.2"
				mockEnv.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
			})

			it("should convert metadata version to top level version in the buildpack plan", func() {
				bpPlan := buildpack.BuildpackPlan{
					Entries: []buildpack.Require{
						{
							Name:     "some-dep",
							Metadata: map[string]interface{}{"version": "v1"},
						},
					},
				}

				_, err := bpTOML.Build(bpPlan, config)
				if err != nil {
					t.Fatalf("Unexpected error:\n%s\n", err)
				}

				testPlan(t,
					[]buildpack.Require{
						{
							Name:     "some-dep",
							Version:  "v1",
							Metadata: map[string]interface{}{"version": "v1"},
						},
					},
					filepath.Join(appDir, "build-plan-in-A-v1.toml"),
				)
			})
		})

		when("buildpack api < 0.5", func() {
			it.Before(func() {
				bpTOML.API = "0.4"
			})

			when("building succeeds", func() {
				it.Before(func() {
					mockEnv.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
				})

				it("should ensure the buildpack's layers dir exists and process build layers", func() {
					h.Mkdir(t,
						filepath.Join(layersDir, "A"),
						filepath.Join(appDir, "layers-A-v1", "layer1"),
						filepath.Join(appDir, "layers-A-v1", "layer2"),
						filepath.Join(appDir, "layers-A-v1", "layer3"),
					)
					h.Mkfile(t, "build = true",
						filepath.Join(appDir, "layers-A-v1", "layer1.toml"),
						filepath.Join(appDir, "layers-A-v1", "layer3.toml"),
					)
					gomock.InOrder(
						mockEnv.EXPECT().AddRootDir(filepath.Join(layersDir, "A", "layer1")),
						mockEnv.EXPECT().AddRootDir(filepath.Join(layersDir, "A", "layer3")),
						mockEnv.EXPECT().AddEnvDir(filepath.Join(layersDir, "A", "layer1", "env"), env.ActionTypePrependPath),
						mockEnv.EXPECT().AddEnvDir(filepath.Join(layersDir, "A", "layer1", "env.build"), env.ActionTypePrependPath),
						mockEnv.EXPECT().AddEnvDir(filepath.Join(layersDir, "A", "layer3", "env"), env.ActionTypePrependPath),
						mockEnv.EXPECT().AddEnvDir(filepath.Join(layersDir, "A", "layer3", "env.build"), env.ActionTypePrependPath),
					)
					if _, err := bpTOML.Build(buildpack.BuildpackPlan{}, config); err != nil {
						t.Fatalf("Unexpected error:\n%s\n", err)
					}
					testExists(t,
						filepath.Join(layersDir, "A"),
					)
				})

				it("should get bom entries and unmet requires from the output buildpack plan", func() {
					bpPlan := buildpack.BuildpackPlan{
						Entries: []buildpack.Require{
							{
								Name:    "some-deprecated-bp-dep",
								Version: "v1", // top-level version is deprecated in buildpack API 0.3
							},
							{
								Name:    "some-deprecated-bp-replace-version-dep",
								Version: "some-version-orig", // top-level version is deprecated in buildpack API 0.3
							},
							{
								Name:     "some-dep",
								Metadata: map[string]interface{}{"version": "v1"},
							},
							{
								Name:     "some-replace-version-dep",
								Metadata: map[string]interface{}{"version": "some-version-orig"},
							},
							{
								Name: "some-unmet-dep",
							},
						},
					}

					h.Mkfile(t,
						"[[entries]]\n"+
							`name = "some-deprecated-bp-dep"`+"\n"+
							`version = "v1"`+"\n"+
							"[[entries]]\n"+
							`name = "some-deprecated-bp-replace-version-dep"`+"\n"+
							`version = "some-version-new"`+"\n"+
							"[[entries]]\n"+
							`name = "some-dep"`+"\n"+
							"[entries.metadata]\n"+
							`version = "v1"`+"\n"+
							"[[entries]]\n"+
							`name = "some-replace-version-dep"`+"\n"+
							"[entries.metadata]\n"+
							`version = "some-version-new"`+"\n",
						filepath.Join(appDir, "build-plan-out-A-v1.toml"),
					)

					br, err := bpTOML.Build(bpPlan, config)
					if err != nil {
						t.Fatalf("Unexpected error:\n%s\n", err)
					}

					if s := cmp.Diff(br, buildpack.BuildResult{
						BOM: []buildpack.BOMEntry{
							{
								Require: buildpack.Require{
									Name:     "some-deprecated-bp-dep",
									Metadata: map[string]interface{}{"version": "v1"},
								},
								Buildpack: buildpack.GroupBuildpack{ID: "A", Version: "v1"},
							},
							{
								Require: buildpack.Require{
									Name:     "some-deprecated-bp-replace-version-dep",
									Metadata: map[string]interface{}{"version": "some-version-new"},
								},
								Buildpack: buildpack.GroupBuildpack{ID: "A", Version: "v1"},
							},
							{
								Require: buildpack.Require{
									Name:     "some-dep",
									Metadata: map[string]interface{}{"version": "v1"},
								},
								Buildpack: buildpack.GroupBuildpack{ID: "A", Version: "v1"},
							},
							{
								Require: buildpack.Require{
									Name:     "some-replace-version-dep",
									Metadata: map[string]interface{}{"version": "some-version-new"},
								},
								Buildpack: buildpack.GroupBuildpack{ID: "A", Version: "v1"},
							},
						},
						Labels: nil,
						MetRequires: []string{
							"some-deprecated-bp-dep",
							"some-deprecated-bp-replace-version-dep",
							"some-dep",
							"some-replace-version-dep",
						},
						Processes: nil,
						Slices:    nil,
					}); s != "" {
						t.Fatalf("Unexpected:\n%s\n", s)
					}
				})

				it("should convert top level version to metadata.version in the bom", func() {
					h.Mkfile(t,
						"[[entries]]\n"+
							`name = "dep-1"`+"\n"+
							`version = "v1"`+"\n"+
							"[[entries]]\n"+
							`name = "dep-2"`+"\n"+
							`version = "v2"`+"\n"+
							"[entries.metadata]\n"+
							`version = "v2"`+"\n"+
							"[[entries]]\n"+
							`name = "dep-3"`+"\n"+
							"[entries.metadata]\n"+
							`version = "v3"`+"\n",
						filepath.Join(appDir, "build-plan-out-A-v1.toml"),
					)

					br, err := bpTOML.Build(buildpack.BuildpackPlan{}, config)
					if err != nil {
						t.Fatalf("Unexpected error:\n%s\n", err)
					}

					if s := cmp.Diff(br.BOM, []buildpack.BOMEntry{
						{
							Require: buildpack.Require{
								Name:     "dep-1",
								Metadata: map[string]interface{}{"version": "v1"},
							},
							Buildpack: buildpack.GroupBuildpack{ID: "A", Version: "v1"},
						},
						{
							Require: buildpack.Require{
								Name:     "dep-2",
								Metadata: map[string]interface{}{"version": "v2"},
							},
							Buildpack: buildpack.GroupBuildpack{ID: "A", Version: "v1"},
						},
						{
							Require: buildpack.Require{
								Name:     "dep-3",
								Metadata: map[string]interface{}{"version": "v3"},
							},
							Buildpack: buildpack.GroupBuildpack{ID: "A", Version: "v1"},
						},
					}); s != "" {
						t.Fatalf("Unexpected:\n%s\n", s)
					}
				})
			})

			when("building fails", func() {
				it("should error when the output buildpack plan is invalid", func() {
					mockEnv.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
					h.Mkfile(t, "bad-key", filepath.Join(appDir, "build-plan-out-A-v1.toml"))
					if _, err := bpTOML.Build(buildpack.BuildpackPlan{}, config); err == nil {
						t.Fatal("Expected error.\n")
					} else if !strings.Contains(err.Error(), "key") {
						t.Fatalf("Incorrect error: %s\n", err)
					}
				})

				it("should error when top level version and metadata version are both present and do not match", func() {
					mockEnv.EXPECT().WithPlatform(platformDir).Return(append(os.Environ(), "TEST_ENV=Av1"), nil)
					h.Mkfile(t,
						"[[entries]]\n"+
							`name = "dep1"`+"\n"+
							`version = "v2"`+"\n"+
							"[entries.metadata]\n"+
							`version = "v1"`+"\n",
						filepath.Join(appDir, "build-plan-out-A-v1.toml"),
					)
					_, err := bpTOML.Build(buildpack.BuildpackPlan{}, config)
					h.AssertNotNil(t, err)
					expected := "top level version does not match metadata version"
					h.AssertStringContains(t, err.Error(), expected)
				})
			})
		})
	})
}

func testExists(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("Error: %s\n", err)
		}
	}
}

func testPlan(t *testing.T, plan []buildpack.Require, paths ...string) {
	t.Helper()
	for _, p := range paths {
		var c struct {
			Entries []buildpack.Require `toml:"entries"`
		}
		if _, err := toml.DecodeFile(p, &c); err != nil {
			t.Fatalf("Error: %s\n", err)
		}
		if s := cmp.Diff(c.Entries, plan); s != "" {
			t.Fatalf("Unexpected plan:\n%s\n", s)
		}
	}
}

func each(it spec.S, befores []func(), text string, f func()) {
	for i := range befores {
		before := befores[i]
		it(fmt.Sprintf("%s #%d", text, i), func() { before(); f() })
	}
}
