package pkg

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

func TestConfigValidateChecksRequiredKeysNotEmpty(t *testing.T) {
	ctx := context.Background()
	gh := NewMockGitHubinator()

	checkEmpty := func(
		ref string, setEmpty func(*Config), expectedErrStr string,
	) {
		fmt.Println(ref)

		testConfig := NewTestConfig()

		setEmpty(testConfig)

		err := testConfig.Validate(ctx, gh)
		assert.ErrorContains(t, err, expectedErrStr)
	}

	checkEmpty(
		"name",
		func(c *Config) {
			c.Watches[0].Name = ""
		},
		"name cannot be empty",
	)

	checkEmpty(
		"user",
		func(c *Config) {
			c.User = ""
		},
		"user cannot be empty",
	)

	checkEmpty(
		"pat",
		func(c *Config) {
			c.PAT = ""
		},
		"pat cannot be empty",
	)

	checkEmpty(
		"watches",
		func(c *Config) {
			c.Watches = []*Watch{}
		},
		"at least one watch",
	)

	checkEmpty(
		"repositories",
		func(c *Config) {
			c.Watches[0].Repositories = []GitHubRepository{}
		},
		"expected at least one repository",
	)

	checkEmpty(
		"selectors",
		func(c *Config) {
			c.Watches[0].selectors = []string{}
			c.Watches[0].bodyRegex = []string{}
			c.Watches[0].RequiredLabels = []string{}
			c.Watches[0].States = []string{}
		},
		"expected at least one filter type",
	)
}

func TestConfigValidateChecksIntervalIsGreaterThanZero(t *testing.T) {
	ctx := context.Background()
	gh := NewMockGitHubinator()
	c := NewTestConfig()

	assert.NilError(t, c.Validate(ctx, gh))

	c.Interval = time.Second * -1
	assert.ErrorContains(t, c.Validate(ctx, gh), "must be greater than zero")
}

func TestConfigValidateChecksValuesWithGitHub(t *testing.T) {
	ctx := context.Background()
	gh := NewMockGitHubinator()
	c := NewTestConfig()

	err := c.Validate(ctx, gh)
	assert.NilError(t, err)

	assert.Assert(t, cmp.Equal(1, gh.WhoAmIRequests))

	assert.Equal(t, 1, len(gh.CheckRepositoryRequests))
	assert.Equal(t, gh.CheckRepositoryRequests[0].Owner, "owner")
	assert.Equal(t, gh.CheckRepositoryRequests[0].Name, "repo")

	getError := errors.New("get failed")

	gh.WhoAmIError = getError
	assert.ErrorContains(t, c.Validate(ctx, gh), "unable to validate pat")
	gh.WhoAmIError = nil

	gh.CheckRepositoryError = getError
	assert.ErrorContains(t, c.Validate(ctx, gh), "unable to validate repository")
	gh.CheckRepositoryError = nil

	gh.WhoAmIReturn = "notauser"
	assert.ErrorContains(t, c.Validate(ctx, gh), "does not match PAT user")
}

func TestWatchValidateChecksSelectorsAreValid(t *testing.T) {
	ctx := context.Background()
	gh := NewMockGitHubinator()
	w := NewTestWatch()

	w.Selectors = []labels.Selector{labels.Nothing()}
	assert.Error(t, w.ValidateAndPopulate(ctx, gh), "got an empty selector")

	badReq, err := labels.NewRequirement(
		"unknown", selection.Exists, []string{},
	)
	assert.NilError(t, err, "unexpected error creating bad req")

	badKeySelector := labels.NewSelector().Add(*badReq)
	w.Selectors = []labels.Selector{badKeySelector}
	assert.ErrorContains(t, w.ValidateAndPopulate(ctx, gh), "unknown key")

	goodReq, err := labels.NewRequirement(
		"type", selection.Exists, []string{},
	)
	assert.NilError(t, err, "unexpected error creating good req")

	goodKeySelector := labels.NewSelector().Add(*goodReq)
	w.Selectors = []labels.Selector{goodKeySelector}
	assert.NilError(t, w.ValidateAndPopulate(ctx, gh), "expected nil error with good key")
}

func TestWatchValidateChecksRegexCanCompile(t *testing.T) {
	ctx := context.Background()
	gh := NewMockGitHubinator()
	w := NewTestWatch()

	w.bodyRegex = []string{".*"}
	assert.NilError(t, w.ValidateAndPopulate(ctx, gh), "unexpected error compiling regex '.*'")

	w.bodyRegex = append(w.bodyRegex, "(")
	assert.ErrorContains(t, w.ValidateAndPopulate(ctx, gh), "unable to compile regex")
}

func TestConfiginatorCanWatchForChanges(t *testing.T) {
	gh := NewMockGitHubinator()

	initialConfig := NewTestConfig()
	initialConfigYAMLBytes, err := yaml.Marshal(initialConfig)
	assert.NilError(t, err)

	initialConfigYAML := string(initialConfigYAMLBytes)

	file, err := ioutil.TempFile("", "config.yaml")
	assert.NilError(t, err)

	_, err = file.Write(initialConfigYAMLBytes)
	assert.NilError(t, err)

	newConfig := NewTestConfig()
	newConfig.PAT = "acoolpat"

	newConfigYAMLBytes, err := yaml.Marshal(newConfig)
	assert.NilError(t, err)

	newConfigYAML := string(newConfigYAMLBytes)

	cnator := NewConfiginator(NewLogger())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)

	defer cancel()

	go func() {
		ticker := time.NewTicker(time.Millisecond * 500)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				file.Truncate(0)
				file.Seek(0, 0)
				_, err = file.Write(newConfigYAMLBytes)
				assert.NilError(t, err)
			}
		}
	}()

	viewedInitialConfig := false
	err = cnator.Watch(ctx, file.Name(), func(observedConfig *Config) {
		// A lot simpler to compare this version of the structs than trying to do a deep equals or the like,
		// since we have some unexported fields.
		observedConfigYAMLBytes, err := yaml.Marshal(observedConfig)
		assert.NilError(t, err)

		observedConfigYAML := string(observedConfigYAMLBytes)

		if !viewedInitialConfig {
			assert.Equal(t, observedConfigYAML, initialConfigYAML)
			viewedInitialConfig = true

			return
		}

		assert.Equal(t, observedConfigYAML, newConfigYAML)
		cancel()
	}, gh)

	assert.ErrorIs(t, err, context.Canceled)
}
