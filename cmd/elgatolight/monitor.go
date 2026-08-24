package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"time"
)

type logEvent struct {
	Time   time.Time       `json:"time"`
	Event  string          `json:"event"`
	Lights []lightSnapshot `json:"lights"`
}

func monitor(ctx context.Context, command string, interval time.Duration, opts options, output io.Writer) error {
	if command == "watch" && opts.json {
		return errors.New("watch is a terminal dashboard; use log for JSON output")
	}
	if command == "watch" && !isTerminal(output) {
		return errors.New("watch requires a terminal; use log for redirected output")
	}

	running, err := startCLIManager(ctx, opts, interval, interval)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer running.cancel()
	previous, err := managedSnapshots(running.manager.ConnectedSnapshot(), true)
	if err != nil {
		return err
	}
	if command == "log" {
		if err := printJSON(output, logEvent{Time: time.Now().UTC(), Event: "initial", Lights: previous}, false); err != nil {
			return err
		}
	} else {
		renderer := lineRenderer{output: output}
		if err := renderer.Render(snapshotTreeLines(previous)); err != nil {
			return err
		}
		return monitorLoop(ctx, command, previous, output, &renderer, running)
	}
	return monitorLoop(ctx, command, previous, output, nil, running)
}

func monitorLoop(
	ctx context.Context,
	command string,
	previous []lightSnapshot,
	output io.Writer,
	renderer *lineRenderer,
	running *runningCLIManager,
) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case runErr := <-running.done:
			if ctx.Err() != nil {
				return nil
			}
			if runErr == nil {
				return errors.New("light manager stopped unexpectedly")
			}
			return runErr
		case event := <-running.events:
			current, err := managedSnapshots(running.manager.ConnectedSnapshot(), true)
			if err != nil {
				return err
			}
			if reflect.DeepEqual(current, previous) {
				continue
			}
			if command == "log" {
				if err := printJSON(output, logEvent{Time: event.Time, Event: "change", Lights: current}, false); err != nil {
					return err
				}
			} else if err := renderer.Render(snapshotTreeLines(current)); err != nil {
				return err
			}
			previous = current
		}
	}
}

type lineRenderer struct {
	output io.Writer
	rows   int
}

func (r *lineRenderer) Render(lines []string) error {
	if r.rows == 0 {
		for _, line := range lines {
			if _, err := fmt.Fprintln(r.output, line); err != nil {
				return err
			}
		}
		r.rows = len(lines)
		return nil
	}
	if _, err := fmt.Fprintf(r.output, "\x1b[%dA", r.rows); err != nil {
		return err
	}
	rows := r.rows
	if len(lines) > rows {
		rows = len(lines)
	}
	for index := 0; index < rows; index++ {
		line := ""
		if index < len(lines) {
			line = lines[index]
		}
		if _, err := fmt.Fprintf(r.output, "\x1b[2K%s\n", line); err != nil {
			return err
		}
	}
	if extra := rows - len(lines); extra > 0 {
		if _, err := fmt.Fprintf(r.output, "\x1b[%dA", extra); err != nil {
			return err
		}
	}
	r.rows = len(lines)
	return nil
}

func isTerminal(output io.Writer) bool {
	file, ok := output.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
