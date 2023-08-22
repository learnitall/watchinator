package pkg

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	e := NewMockEmailinator()

	checkEmpty := func(
		ref string, setEmpty func(*Config), expectedErrStr string,
	) {
		fmt.Println(ref)

		testConfig := NewTestConfig()

		setEmpty(testConfig)

		err := testConfig.Validate(ctx, gh, e)
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
			c.Watches[0].Selectors = []string{}
			c.Watches[0].BodyRegex = []string{}
			c.Watches[0].RequiredLabels = []string{}
			c.Watches[0].States = []string{}
		},
		"expected at least one filter type",
	)

	checkEmpty(
		"email username",
		func(c *Config) {
			c.Email.Username = ""
		},
		"username cannot be empty",
	)

	checkEmpty(
		"email password",
		func(c *Config) {
			c.Email.Password = ""
		},
		"password cannot be empty",
	)

	checkEmpty(
		"email host",
		func(c *Config) {
			c.Email.Host = ""
		},
		"host cannot be empty",
	)

	checkEmpty(
		"email port",
		func(c *Config) {
			c.Email.Port = 0
		},
		"port must be",
	)
}

func TestConfigValidateEnsuresEmailPortIsTLSOrSSL(t *testing.T) {
	ctx := context.Background()
	gh := NewMockGitHubinator()
	e := NewMockEmailinator()
	c := NewTestConfig()

	assert.NilError(t, c.Validate(ctx, gh, e))

	c.Email.Port = 587
	assert.NilError(t, c.Validate(ctx, gh, e))

	c.Email.Port = 465
	assert.NilError(t, c.Validate(ctx, gh, e))

	c.Email.Port = 123
	assert.ErrorContains(t, c.Validate(ctx, gh, e), "port must be")
}

func TestConfigValidatesCanCreateNewEmail(t *testing.T) {
	ctx := context.Background()
	gh := NewMockGitHubinator()
	e := NewMockEmailinator()
	c := NewTestConfig()

	assert.NilError(t, c.Validate(ctx, gh, e))

	e.NewMsgError = errors.New("my error")
	assert.ErrorContains(t, c.Validate(ctx, gh, e), "my error")
}

func TestConfigValidateChecksIntervalIsGreaterThanZero(t *testing.T) {
	ctx := context.Background()
	gh := NewMockGitHubinator()
	e := NewMockEmailinator()
	c := NewTestConfig()

	assert.NilError(t, c.Validate(ctx, gh, e))

	c.Interval = time.Second * -1
	assert.ErrorContains(t, c.Validate(ctx, gh, e), "must be greater than zero")
}

func TestConfigValidateChecksValuesWithGitHub(t *testing.T) {
	ctx := context.Background()
	gh := NewMockGitHubinator()
	e := NewMockEmailinator()
	c := NewTestConfig()

	err := c.Validate(ctx, gh, e)
	assert.NilError(t, err)

	assert.Assert(t, cmp.Equal(1, gh.WhoAmIRequests))

	assert.Equal(t, 1, len(gh.CheckRepositoryRequests))
	assert.Equal(t, gh.CheckRepositoryRequests[0].Owner, "owner")
	assert.Equal(t, gh.CheckRepositoryRequests[0].Name, "repo")

	getError := errors.New("get failed")

	gh.WhoAmIError = getError
	assert.ErrorContains(t, c.Validate(ctx, gh, e), "unable to validate pat")
	gh.WhoAmIError = nil

	gh.CheckRepositoryError = getError
	assert.ErrorContains(t, c.Validate(ctx, gh, e), "unable to validate repository")
	gh.CheckRepositoryError = nil

	gh.WhoAmIReturn = "notauser"
	assert.ErrorContains(t, c.Validate(ctx, gh, e), "does not match PAT user")
}

func TestWatchValidateChecksSelectorsAreValid(t *testing.T) {
	ctx := context.Background()
	gh := NewMockGitHubinator()
	w := NewTestWatch()

	badReq, err := labels.NewRequirement(
		"unknown", selection.Exists, []string{},
	)
	assert.NilError(t, err, "unexpected error creating bad req")

	badKeySelector := labels.NewSelector().Add(*badReq)
	w.Selectors = []string{badKeySelector.String()}
	assert.ErrorContains(t, w.ValidateAndPopulate(ctx, gh), "unknown key")

	goodReq, err := labels.NewRequirement(
		"type", selection.Exists, []string{},
	)
	assert.NilError(t, err, "unexpected error creating good req")

	goodKeySelector := labels.NewSelector().Add(*goodReq)
	w.Selectors = []string{goodKeySelector.String()}
	assert.NilError(t, w.ValidateAndPopulate(ctx, gh), "expected nil error with good key")
}

func TestWatchValidateChecksRegexCanCompile(t *testing.T) {
	ctx := context.Background()
	gh := NewMockGitHubinator()
	w := NewTestWatch()

	w.BodyRegex = []string{".*"}
	assert.NilError(t, w.ValidateAndPopulate(ctx, gh), "unexpected error compiling regex '.*'")

	w.BodyRegex = append(w.BodyRegex, "(")
	assert.ErrorContains(t, w.ValidateAndPopulate(ctx, gh), "unable to compile regex")
}

func TestEmailValidateChecksConnection(t *testing.T) {
	ctx := context.Background()
	e := NewMockEmailinator()
	emailConfig := NewTestConfig().Email

	assert.NilError(t, emailConfig.Validate(ctx, e), "unexpected error checking email connection")

	e.TestConnectionError = errors.New("my test error")

	assert.ErrorContains(
		t, emailConfig.Validate(ctx, e), "my test error",
	)
}

func TestConfiginatorCanWatchForChanges(t *testing.T) {
	gh := NewMockGitHubinator()
	e := NewMockEmailinator()

	initialConfig := NewTestConfig()
	initialConfigYAMLBytes, err := yaml.Marshal(initialConfig)
	assert.NilError(t, err)

	initialConfigYAML := string(initialConfigYAMLBytes)

	file, err := os.CreateTemp("", "config.yaml")
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
				assert.NilError(t, file.Truncate(0))

				_, err = file.Seek(0, 0)
				assert.NilError(t, err)

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
	}, gh, e)

	assert.ErrorIs(t, err, context.Canceled)
}
