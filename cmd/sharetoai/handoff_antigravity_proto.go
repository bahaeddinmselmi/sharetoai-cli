package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"time"
)

// Antigravity's step_payload blobs are raw protobuf wire format with no
// published .proto schema. This is a minimal hand-written encoder covering
// exactly the fields this package needs to emit — it was reverse-engineered
// by decoding real Antigravity CLI conversation databases and live-tested
// against a real installed `agy` binary: a planted, otherwise-nonexistent
// token written this way was correctly recalled on `agy --conversation
// <id>` with zero tool calls, proving the model genuinely resumed from this
// encoded history rather than finding it some other way (e.g. a background
// search tool). See docs/superpowers/specs/2026-07-20-antigravity-real-
// injection-design.md for the full investigation.

func protoVarint(buf *bytes.Buffer, n uint64) {
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			buf.WriteByte(b | 0x80)
		} else {
			buf.WriteByte(b)
			return
		}
	}
}

func protoTag(buf *bytes.Buffer, fieldNum int, wireType int) {
	protoVarint(buf, uint64(fieldNum)<<3|uint64(wireType))
}

// protoVarintField appends a wire-type-0 (varint) field.
func protoVarintField(buf *bytes.Buffer, fieldNum int, n uint64) {
	protoTag(buf, fieldNum, 0)
	protoVarint(buf, n)
}

// protoBytesField appends a wire-type-2 (length-delimited) field — used for
// both raw byte/string content and nested submessages, since a protobuf
// submessage is just its own encoded bytes wrapped the same way.
func protoBytesField(buf *bytes.Buffer, fieldNum int, data []byte) {
	protoTag(buf, fieldNum, 2)
	protoVarint(buf, uint64(len(data)))
	buf.Write(data)
}

func protoStringField(buf *bytes.Buffer, fieldNum int, s string) {
	protoBytesField(buf, fieldNum, []byte(s))
}

// protoTimestamp encodes a google.protobuf.Timestamp-shaped submessage:
// field 1 = seconds, field 2 = nanos. Confirmed against real Antigravity CLI
// samples (e.g. a real step's creation timestamp decoded to exactly this
// two-field shape).
func protoTimestamp(t time.Time) []byte {
	var buf bytes.Buffer
	protoVarintField(&buf, 1, uint64(t.Unix()))
	protoVarintField(&buf, 2, uint64(t.Nanosecond()))
	return buf.Bytes()
}

// newAntigravityUUID generates a random UUIDv4-shaped identifier. Used for
// per-step ids and (in handoff_antigravity_inject.go) conversation/
// trajectory ids — Antigravity's real ids are UUIDv4-shaped but nothing in
// the live-tested resume path checked the version/variant nibbles
// specifically, unlike Codex's UUIDv7 requirement (see newCodexSessionID in
// handoff_codex.go) — a standard random UUID shape is enough here.
func newAntigravityUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Antigravity's step-metadata "source" enum, confirmed by decoding real
// conversations: MODEL responses use 2, explicit user input uses 4.
const (
	antigravitySourceModel = 2
	antigravitySourceUser  = 4
)

// antigravityStepMeta builds the envelope every real step_payload carries
// in its top-level field 5: a creation timestamp, the source enum, a fresh
// per-step UUID, a back-reference to {trajectory_id, step index,
// conversation_id} (field 20), and a one-entry status-history list marking
// the step DONE (state 3, field 26). This exact shape is what was
// live-tested successfully.
func antigravityStepMeta(trajID, convID string, idx int, source int, when time.Time) []byte {
	var backref bytes.Buffer
	protoStringField(&backref, 1, trajID)
	protoVarintField(&backref, 2, uint64(idx))
	protoVarintField(&backref, 3, 2)
	protoStringField(&backref, 4, convID)

	var statusEntry bytes.Buffer
	protoVarintField(&statusEntry, 1, 3) // state 3 = DONE
	protoBytesField(&statusEntry, 2, protoTimestamp(when))

	var statusHistory bytes.Buffer
	protoBytesField(&statusHistory, 1, statusEntry.Bytes())

	var meta bytes.Buffer
	protoBytesField(&meta, 1, protoTimestamp(when))
	protoVarintField(&meta, 3, uint64(source))
	protoStringField(&meta, 12, newAntigravityUUID())
	protoBytesField(&meta, 20, backref.Bytes())
	protoBytesField(&meta, 26, statusHistory.Bytes())
	protoBytesField(&meta, 32, protoTimestamp(when))
	return meta.Bytes()
}

// antigravityUserStepPayload builds a step_type=14 (USER_INPUT) protobuf
// step_payload. Content lives in top-level field 19, nested field 2.
func antigravityUserStepPayload(trajID, convID string, idx int, text string, when time.Time) []byte {
	var body bytes.Buffer
	protoStringField(&body, 2, text)

	var payload bytes.Buffer
	protoVarintField(&payload, 1, 14) // step_type
	protoVarintField(&payload, 4, 3)  // status = DONE
	protoBytesField(&payload, 5, antigravityStepMeta(trajID, convID, idx, antigravitySourceUser, when))
	protoBytesField(&payload, 19, body.Bytes())
	return payload.Bytes()
}

// antigravityModelStepPayload builds a step_type=15 (PLANNER_RESPONSE)
// protobuf step_payload. Content lives in top-level field 20: nested field
// 1 is the response text, field 6 a fresh "bot-<uuid>" identifier, field 8
// the same text again — matching the duplication seen in real samples.
func antigravityModelStepPayload(trajID, convID string, idx int, text string, when time.Time) []byte {
	var body bytes.Buffer
	protoStringField(&body, 1, text)
	protoStringField(&body, 6, "bot-"+newAntigravityUUID())
	protoStringField(&body, 8, text)

	var payload bytes.Buffer
	protoVarintField(&payload, 1, 15) // step_type
	protoVarintField(&payload, 4, 3)  // status = DONE
	protoBytesField(&payload, 5, antigravityStepMeta(trajID, convID, idx, antigravitySourceModel, when))
	protoBytesField(&payload, 20, body.Bytes())
	return payload.Bytes()
}
