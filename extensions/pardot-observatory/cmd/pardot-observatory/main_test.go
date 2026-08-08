package main

import (
	"context"
	"fmt"
	"testing"

	"lisanalgaib/internal/connectors"
)

func TestObservatoryBoundsRememberedJobsAndNotes(t *testing.T) {
	t.Setenv("LISAN_EXTENSION_STATE", "")
	service := newObservatory()
	for index := 0; index < maxRememberedJobs+maxNotes+8; index++ {
		job, err := service.StartJob(context.Background(), connectors.StartJobRequest{
			ActionID: "record-note",
			Inputs:   map[string]string{"note": fmt.Sprintf("note-%03d", index)},
		})
		if err != nil || job.Status != connectors.JobSucceeded {
			t.Fatalf("record note %d: job=%#v err=%v", index, job, err)
		}
	}
	if len(service.jobs) != maxRememberedJobs {
		t.Fatalf("remembered jobs = %d, want %d", len(service.jobs), maxRememberedJobs)
	}
	if len(service.notes) != maxNotes || service.notes[0] != "note-136" {
		t.Fatalf("bounded notes were not the newest %d entries: first=%q count=%d", maxNotes, service.notes[0], len(service.notes))
	}
}

func TestObservatoryRejectsWorkWhenEveryJobSlotIsActive(t *testing.T) {
	t.Setenv("LISAN_EXTENSION_STATE", "")
	service := newObservatory()
	for index := 0; index < maxRememberedJobs; index++ {
		id := fmt.Sprintf("job-%06d", index)
		service.jobs[id] = &surveyJob{job: connectors.Job{ID: id, ActionID: "survey", Status: connectors.JobRunning}}
	}
	if _, err := service.StartJob(context.Background(), connectors.StartJobRequest{ActionID: "record-note", Inputs: map[string]string{"note": "bounded"}}); err == nil {
		t.Fatal("extension accepted work after every bounded job slot became active")
	}
}

func TestObservatoryBoundsOpenSessions(t *testing.T) {
	t.Setenv("LISAN_EXTENSION_STATE", "")
	service := newObservatory()
	request := connectors.OpenSessionRequest{SessionID: "field-console", Rows: 24, Columns: 80}
	for index := 0; index < maxOpenSessions; index++ {
		if _, err := service.OpenSession(context.Background(), request); err != nil {
			t.Fatalf("open session %d: %v", index, err)
		}
	}
	if _, err := service.OpenSession(context.Background(), request); err == nil {
		t.Fatal("extension exceeded its open-session bound")
	}
}
