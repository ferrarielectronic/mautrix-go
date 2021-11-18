// Copyright (c) 2021 Sumner Evans
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package appservice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type MessageSendCheckpointStep string

const (
	StepClient     MessageSendCheckpointStep = "CLIENT"
	StepHomeserver MessageSendCheckpointStep = "HOMESERVER"
	StepBridge     MessageSendCheckpointStep = "BRIDGE"
	StepDecrypted  MessageSendCheckpointStep = "DECRYPTED"
	StepRemote     MessageSendCheckpointStep = "REMOTE"
	StepCommand    MessageSendCheckpointStep = "COMMAND"
)

type MessageSendCheckpointStatus string

const (
	StatusSuccesss    MessageSendCheckpointStatus = "SUCCESS"
	StatusWillRetry   MessageSendCheckpointStatus = "WILL_RETRY"
	StatusPermFailure MessageSendCheckpointStatus = "PERM_FAILURE"
)

type MessageSendCheckpointReportedBy string

const (
	ReportedByAsmux  MessageSendCheckpointReportedBy = "ASMUX"
	ReportedByBridge MessageSendCheckpointReportedBy = "BRIDGE"
)

type MessageSendCheckpoint struct {
	EventID     id.EventID                      `json:"event_id"`
	RoomID      id.RoomID                       `json:"room_id"`
	Step        MessageSendCheckpointStep       `json:"step"`
	Timestamp   time.Time                       `json:"timestamp"`
	Status      MessageSendCheckpointStatus     `json:"status"`
	EventType   event.Type                      `json:"event_type"`
	ReportedBy  MessageSendCheckpointReportedBy `json:"reported_by"`
	RetryNum    int                             `json:"retry_num"`
	MessageType event.MessageType               `json:"message_type,omitempty"`
	Info        string                          `json:"info,omitempty"`
}

var CheckpointTypes = map[event.Type]interface{}{
	event.EventRedaction:   struct{}{},
	event.EventMessage:     struct{}{},
	event.EventEncrypted:   struct{}{},
	event.EventSticker:     struct{}{},
	event.EventReaction:    struct{}{},
	event.CallInvite:       struct{}{},
	event.CallCandidates:   struct{}{},
	event.CallSelectAnswer: struct{}{},
	event.CallAnswer:       struct{}{},
	event.CallHangup:       struct{}{},
	event.CallReject:       struct{}{},
	event.CallNegotiate:    struct{}{},
}

func NewMessageSendCheckpoint(evt *event.Event, step MessageSendCheckpointStep, status MessageSendCheckpointStatus) *MessageSendCheckpoint {
	checkpoint := MessageSendCheckpoint{
		EventID:    evt.ID,
		RoomID:     evt.RoomID,
		Step:       step,
		Timestamp:  time.Now(),
		Status:     status,
		EventType:  evt.Type,
		ReportedBy: ReportedByBridge,
	}
	if evt.Type == event.EventMessage {
		checkpoint.MessageType = evt.Content.AsMessage().MsgType
	}
	return &checkpoint
}

func (as *AppService) SendMessageSendCheckpoint(evt *event.Event, step MessageSendCheckpointStep) {
	checkpoint := NewMessageSendCheckpoint(evt, step, StatusSuccesss)
	go checkpoint.Send(as)
}

func (as *AppService) SendErrorMessageSendCheckpoint(evt *event.Event, step MessageSendCheckpointStep, err error, permanent bool) {
	status := StatusWillRetry
	if permanent {
		status = StatusPermFailure
	}
	checkpoint := NewMessageSendCheckpoint(evt, step, status)
	checkpoint.Info = err.Error()
	go checkpoint.Send(as)
}

func (cp *MessageSendCheckpoint) Send(as *AppService) error {
	return SendCheckpoints(as, []*MessageSendCheckpoint{cp})
}

type CheckpointsJSON struct {
	Checkpoints []*MessageSendCheckpoint `json:"checkpoints"`
}

func SendCheckpoints(as *AppService, checkpoints []*MessageSendCheckpoint) error {
	checkpointsJSON := CheckpointsJSON{Checkpoints: checkpoints}

	if as.HasWebsocket() {
		return as.SendWebsocket(&WebsocketRequest{
			Command: "message_checkpoint",
			Data:    checkpointsJSON,
		})
	}

	if as.MessageSendCheckpointEndpoint == "" {
		return nil
	}

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(checkpointsJSON); err != nil {
		return fmt.Errorf("failed to encode message send checkpoint JSON: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, as.MessageSendCheckpointEndpoint, &body)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+as.Registration.AppToken)
	req.Header.Set("User-Agent", mautrix.DefaultUserAgent+" checkpoint sender")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Failed to send bridge state update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		respBody, _ := ioutil.ReadAll(resp.Body)
		if respBody != nil {
			respBody = bytes.ReplaceAll(respBody, []byte("\n"), []byte("\\n"))
		}
		return fmt.Errorf("Unexpected status code %d sending bridge state update: %s", resp.StatusCode, respBody)
	}
	return nil
}