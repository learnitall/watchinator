# watchinator

[![Go Reference](https://pkg.go.dev/badge/github.com/learnitall/watchinator.svg)](https://pkg.go.dev/github.com/learnitall/watchinator)
[![MIT License](https://img.shields.io/badge/license-MIT-blue)](https://github.com/learnitall/watchinator/blob/main/LICENSE)
[![Report Card](https://goreportcard.com/badge/github.com/learnitall/watchinator)](https://goreportcard.com/report/github.com/learnitall/watchinator)
[![Release](https://img.shields.io/github/release/learnitall/watchinator.svg)](https://github.com/learnitall/watchinator/releases/latest)

Subscribe to things on GitHub using custom filters.

## Quick Start

1. Create a GitHub PAT (classic) to allow watchinator to view and subscribe to things on your behalf.
   The following scopes are needed:
     1. For public repositories: **public_repo**
     2. For private repositories: **repo**
     3. **notifications**

2. Create a new config:

```yaml
user: your_username
pat: your_pat
interval: 30m
watches:
- name: "bug issues on cilium/cilium"
  repos:
    - name: "cilium"
      owner: "cilium"
  states:
    - OPEN
  requiredLabels:
    - "bug"
  searchLabels:
    - "bug"
  actions:
    subscribe:
      enabled: true
```

3. Validate the config:

```
$ go run . validate-config --config ./config.yaml
$ go run . whoami --config ./config.yaml
```

4. Run:

```
$ go run . watch --config ./config.yaml
```

To build and run using nix:

```
$ nix run '.#watchinator'
```

As a container:

```
$ nix build '.#watchinator-image'
$ cat result | docker load
```

## Overview

Watchinator was created to handle subscribing to select issues on massive repositories with a lot of collaborators.
GitHub only has support for automatically subscribing to all issues on a repository, which can create notification fatigue and
generate noise in an inbox.

Watchinator will continually poll GitHub for new issues and filter them based on pre-configured criteria. If an issue matches
the pre-configured criteria, then watchinator will perform pre-configured actions, such as subscribing the user to the issue or
sending an email to the user containing the issue.

## Configuration

Watchinator is configured using a yaml-configurtion file. Documentation is provided in the associated go-struct. When watchinator
starts up, validation is performed on the configuration to ensure things are set correctly.

Each config file is composed of multiple 'Watches'. A 'Watch' describes a set of match criteria which will be applied to
the watch's configured repositories, and a set of actions which will be performed on a match.

### Example

In this example, we'll configure a watch that uses each of the available criteria. We first need to start by populating our
top-level config fields:

* **User**: Your GitHub username. This is required to ensure authentication is working properly.
* **PAT**: A PAT which can be used to authenticate to GitHub. See the quick start section above for information on the required
           scopes.
* **Interval**: The amount of time in-between querying GitHub for issues to subscribe to. This field is parsed using
                the function [time.ParseDuration](https://pkg.go.dev/time#ParseDuration).

Overall this will look like:

```yaml
user: your_username
pat: your_pat
interval: some_interval
```

To ensure that authentication is working properly, you can use the 'whoami' subcommand:

```
$ go run . --config ./config.yaml whoami
time=2023-05-02T11:52:24.507-06:00 level=INFO source=/home/rydrew/Documents/watchinator/cmd/whoami.go:47 msg="Hello learnitall!"
```

Now let's create our 'Watch'. Let's start by giving our watch a name. Each 'Watch' is uniquely identified by this field and it
is referenced in debug logs (toggle-able with `--verbose`).

```yaml
watches: 
- name: "example" 
```

Next we can specify our repository. In this case, we can use "learnitall/watchinator"

```yaml
watches:
- name: "example"
  repos:
    - name: "watchinator"
      owner: "learnitall"
```

Let's add our first filter by using the 'states' option to only target issues that are currently open:

```yaml
watches:
- name: "example"
  repos:
    - name: "watchinator"
      owner: "learnitall"
  states:
    - OPEN
```

Watchinator uses the [shurcooL/githubv4](https://github.com/shurcooL/githubv4) library in the background to perform
queries against GitHub's GraphQL API. As such, the valid options for the state field can be found
[here](https://docs.github.com/en/graphql/reference/enums#issuestate).

Now that we have a watch with at least one filter, we can use watchinator's 'list' subcommand to test it out:

```
$ go run . list example --config ./config.yaml
```

> The output here is in JSON, so feel free to pipe it to jq.

Let's move on to trying out more filters to target issue #1
[This is a Test Issue](https://github.com/learnitall/watchinator/issues/1).

First, labels. This issue has two labels `bug` and `wontfix`. We can configure watchinator to target issues that have BOTH
of these labels by adding a 'requiredLabels' field to our config:

```yaml
watches:
- name: "example"
  repos:
    - name: "watchinator"
      owner: "learnitall"
  states:
    - OPEN
  requiredLabels:
    - "bug"
    - "wontfix"
```

For repositories with lots of issues, we can tone down the number of requests made to GitHub by also configuring the
'searchLabels' field. This will ask GitHub to only return issues to the watchinator that contain at least one of these
labels:

```yaml
watches:
- name: "example"
  repos:
    - name: "watchinator"
      owner: "learnitall"
  states:
    - OPEN
  requiredLabels:
    - "bug"
    - "wontfix"
  searchLabels:
    - "bug"
```

> Some filters are available within the GitHub API, such as states and search labels. Others are implemented in the
> watchinator itself.

Let's further target issue #1 by setting a regex filter on the issue's body. The issue body is:

```
Can the watchinator subscribe to this issue?
```

so we can make a crude regex filter to target it:

```
^Can the watchinator subscribe to this issue\?$
```

and add it into our config:

```yaml
watches:
- name: "example"
  repos:
    - name: "watchinator"
      owner: "learnitall"
  states:
    - OPEN
  requiredLabels:
    - "bug"
    - "wontfix"
  searchLabels:
    - "bug"
  bodyRegex:
    - "^Can the watchinator subscribe to this issue\\?$"
```

This is a pretty specific set of criteria, but we can be incredibly specific by adding a metadata selector. After each
issue is pulled from GitHub, it is converted into a set of selectable metadata. The 'selectors' field follows the
Kubernetes label selector syntax (defined [here](https://pkg.go.dev/k8s.io/apimachinery@v0.27.1/pkg/labels#Parse)). To find
selectable metadata, look for the function `GitHubItemAsLabelSet`.

In this case, we can select the issue's number:

```yaml
watches:
- name: "example"
  repos:
    - name: "watchinator"
      owner: "learnitall"
  states:
    - OPEN
  requiredLabels:
    - "bug"
    - "wontfix"
  searchLabels:
    - "bug"
  bodyRegex:
    - "^Can the watchinator subscribe to this issue\\?$"
  selectors
    - "number==1"
```

Running a 'list' should return only a single issue:

```json
[
  {
    "author": {
      "login": "learnitall"
    },
    "body": "Can the watchinator subscribe to this issue?",
    "labels": [
      "bug",
      "wontfix"
    ],
    "number": 1,
    "state": "OPEN",
    "title": "This is a Test Issue",
    "Subscription": "IGNORED",
    "type": "issue",
    "repo": {
      "owner": "learnitall",
      "name": "watchinator"
    },
    "id": "I_kwDOJcp8Zs5krjB2"
  }
]
```

Finally, let's tell watchinator what to do when it finds a new issue. In this example, let's ask watchinator to ensure we are
subscribed to matched issues:

```yaml
watches:
- name: "example"
  repos:
    - name: "watchinator"
      owner: "learnitall"
  states:
    - OPEN
  requiredLabels:
    - "bug"
    - "wontfix"
  searchLabels:
    - "bug"
  bodyRegex:
    - "^Can the watchinator subscribe to this issue\\?$"
  selectors
    - "number==1"
  actions:
    subscribe:
      enabled: true
```

This action will be performed when watchinator is kicked off using the 'watch' subcommand, which will continually poll GitHub
for issues using the interval we configured earlier.

Another action we can have the watchinator take is send us an email for each matched issue we aren't subscribed to. This can be
useful, as GitHub will not notify us if we subscribe to a new issue, only when a subscribed issue has an update. To
configure the email action, first let's teach watchinator how to send an email from a gmail account.

To do this, we first need to generate a 16 character [app-specific password](https://support.google.com/accounts/answer/185833?hl=en)
for the account. Once we have this, we can add it into our config, along with information on how to connect to gmail's
smtp service:

```yaml
user: your_username
pat: your_pat
interval: some_interval
email:
  username: "myemail@gmail.com"
  password: "1234567890123456"
  host: "smtp.gmail.com"
  port: 587
```

After this, we just need to add the email action into our watch configuration:

```
watches:
- name: "example"
  repos:
    - name: "watchinator"
      owner: "learnitall"
  states:
    - OPEN
  requiredLabels:
    - "bug"
    - "wontfix"
  searchLabels:
    - "bug"
  bodyRegex:
    - "^Can the watchinator subscribe to this issue\\?$"
  selectors
    - "number==1"
  actions:
    subscribe:
      enabled: true
    email:
      enabled: true
      sendTo: "myotheremail@gmail.com"
```

The next time watchinator is started using the 'watch' subcommand, when it finds an issue on GitHub that we haven't subscribed to yet
that matches our criteria it will send an email to "myotheremail@gmail.com" from "myemail@gmail.com" containing the issue formatted
as JSON.

## Installation

> To be filled out

## Development

### Building

[Nix](https://nixos.org/) is used as the main build system. The provided [flake](https://nixos.wiki/wiki/Flakes) can be used
to build both the watchinator binary and container image.

#### Build-Time Variables

The following variables should be set a build-time using ldflags:

* `github.com/learnitall/watchinator/cmd.commit`: The current git commit SHA at the time of build, or 'dirty' if the git
  working tree is dirty.
* `github.com/learnitall/watchinator/cmd.tag`: The git tag the build is based off of. This should be the same as the
  binary's associated container image tag.
