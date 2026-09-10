// `owl pr --check`: what has arrived in your court since owl last
// looked. One shot, no TUI — for a scheduler, because the list itself
// is only awake while you are watching it, and a review that lands
// while the popup is shut is exactly the one worth telling you about.
//
// What it announces is what nothing else does. owl's other notion of
// attention includes an agent blocked or done-unread, but Claude Code
// already posts those to the multiplexer's own notifications; owl
// repeating them would tell you twice. This is the half nobody else
// watches: work arriving from GitHub.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// inYourCourt reports whether a PR is waiting on the user: theirs to
// review, or theirs to look at again since the author pushed. The same
// two buckets the list puts at the top and `n` jumps to.
func inYourCourt(pr PR, me string) bool {
	switch pr.MyReviewStatus(me) {
	case StatusTodo, StatusWaitingForYou:
		return true
	}
	return false
}

// arrived returns the PRs that are in the user's court now and were
// not last time, newest first.
//
// A transition and not a state: a PR sitting in Todo for a week is not
// news, and announcing the standing set on every run would make the
// notification worth ignoring by the second day.
func arrived(before, now []PR, me string) []PR {
	was := make(map[int]bool, len(before))
	for _, pr := range before {
		if inYourCourt(pr, me) {
			was[pr.Number] = true
		}
	}
	var out []PR
	for _, pr := range now {
		if inYourCourt(pr, me) && !was[pr.Number] {
			out = append(out, pr)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number > out[j].Number })
	return out
}

// runCheck fetches the PR list, says what has arrived since the last
// look, and leaves the cache as the new baseline.
//
// The baseline is the same cache the list reads, so opening owl counts
// as having seen what was in it. That is deliberate: a notification
// about a row you were looking at a moment ago is noise.
func runCheck(cfg Config, out io.Writer) error {
	repo := currentRepo(cfg.Remote)
	if repo == "" {
		return fmt.Errorf("no GitHub repo: the working directory has no %q remote", cfg.Remote)
	}
	me := currentUser()
	if me == "" {
		return fmt.Errorf("gh could not say who you are; `gh auth status` will")
	}
	prs, err := openPRs(repo)
	if err != nil {
		return err
	}

	// No cache at all is a first run, not a morning's worth of news:
	// everything in your court would read as having just arrived. Take
	// the baseline and say nothing.
	// No cache at all is a first run, not a morning's worth of news:
	// everything in your court would read as having just arrived. The
	// count is still true, though, so the hook still hears it — a badge
	// should be right from the first run, and only the announcement is
	// suppressed.
	cached := loadCache(repo)
	updated := cacheFile{Prs: prs, Me: me, FetchedAt: time.Now()}
	var news []PR
	if cached != nil {
		news = arrived(cached.Prs, prs, me)
		// Everything else in the file is the list's, not the check's:
		// this runs from a scheduler behind your back, and wiping the
		// merged section or moving the cursor is not its business.
		updated = *cached
		updated.Prs, updated.Me, updated.FetchedAt = prs, me, time.Now()
	}

	// The baseline moves whether or not anything arrived, and before
	// the hook runs: a hook that fails should not make owl announce the
	// same PR again on the next run.
	saveCache(repo, updated)

	waiting := 0
	for _, pr := range prs {
		if inYourCourt(pr, me) {
			waiting++
		}
	}
	for _, pr := range news {
		fmt.Fprintf(out, "#%-5d %-16s %s\n", pr.Number, pr.MyReviewStatus(me), trim(pr.Title, 60))
	}
	return announce(cfg, repo, news, waiting, out)
}

// announce reports where things stand: the configured hook if there is
// one, else the multiplexer's own notification.
//
// The two are shaped differently on purpose, because they are
// different things. A notification is an **event** — it fires when
// work arrives and stays quiet otherwise, since one every five minutes
// saying the same thing is not a notification. A hook is asked about
// **state**, every run, arrival or not: a badge that can go up must be
// able to come down, and a hook that only hears about arrivals can
// raise a count it will never clear.
//
// The hook is also the agnostic half. owl knows about tmux, herdr and
// cmux and should keep knowing only those — a desktop notifier, a
// sidebar badge, a light on a shelf are a command with the numbers in
// its environment.
func announce(cfg Config, repo string, news []PR, waiting int, out io.Writer) error {
	numbers := make([]string, 0, len(news))
	for _, pr := range news {
		numbers = append(numbers, strconv.Itoa(pr.Number))
	}
	summary := fmt.Sprintf("%d waiting for you in %s", waiting, repo)
	switch {
	case len(news) == 1:
		summary = fmt.Sprintf("#%s %s — %d waiting for you", numbers[0], trim(news[0].Title, 40), waiting)
	case waiting == 0:
		summary = "nothing waiting for you in " + repo
	}

	mx := newWindows(cfg, reviews)
	hook := cfg.Hooks.Attention.For(mx.Kind())
	if hook == "" {
		if len(news) > 0 {
			mx.Notify(summary)
		}
		return nil
	}
	env := map[string]string{
		"OWL_ARRIVED":     strconv.Itoa(len(news)),
		"OWL_ARRIVED_PRS": strings.Join(numbers, ","),
		"OWL_WAITING":     strconv.Itoa(waiting),
		"OWL_SUMMARY":     summary,
		"OWL_REPO":        repo,
		"OWL_MUX":         mx.Kind(),
	}
	cmd := exec.Command("sh", "-c", hook)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hooks.attention: %w", err)
	}
	return nil
}
