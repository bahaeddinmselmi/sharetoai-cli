package main

import (
	"strings"
	"testing"
	"time"
)

// protoField is a single decoded protobuf wire-format field, used only by
// tests in this package to verify the hand-written encoder in
// handoff_antigravity_proto.go round-trips correctly. It intentionally
// supports only the two wire types this encoder ever emits (varint and
// length-delimited) — Antigravity's real step_payload format uses others
// (fixed32/fixed64) in fields we never write, so a fuller decoder isn't
// needed here.
type protoField struct {
	Num      int
	WireType int
	Varint   uint64
	Bytes    []byte
}

func decodeVarint(data []byte) (uint64, int) {
	var result uint64
	var shift uint
	for i, b := range data {
		result |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return result, i + 1
		}
		shift += 7
	}
	return result, len(data)
}

func decodeProtoFields(t *testing.T, data []byte) []protoField {
	t.Helper()
	var fields []protoField
	i := 0
	for i < len(data) {
		tag, n := decodeVarint(data[i:])
		i += n
		fieldNum := int(tag >> 3)
		wireType := int(tag & 0x7)
		switch wireType {
		case 0:
			v, n := decodeVarint(data[i:])
			i += n
			fields = append(fields, protoField{Num: fieldNum, WireType: 0, Varint: v})
		case 2:
			ln, n := decodeVarint(data[i:])
			i += n
			b := data[i : i+int(ln)]
			i += int(ln)
			fields = append(fields, protoField{Num: fieldNum, WireType: 2, Bytes: b})
		default:
			t.Fatalf("unsupported wire type %d at field %d", wireType, fieldNum)
		}
	}
	return fields
}

func findField(fields []protoField, num int) (protoField, bool) {
	for _, f := range fields {
		if f.Num == num {
			return f, true
		}
	}
	return protoField{}, false
}

func TestAntigravityUserStepPayload_ContentRoundTrips(t *testing.T) {
	when := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	payload := antigravityUserStepPayload("traj-1", "conv-1", 0, "hello from the user", when)

	fields := decodeProtoFields(t, payload)

	stepType, ok := findField(fields, 1)
	if !ok || stepType.Varint != 14 {
		t.Fatalf("expected field 1 (step_type) = 14, got %+v (ok=%v)", stepType, ok)
	}

	status, ok := findField(fields, 4)
	if !ok || status.Varint != 3 {
		t.Fatalf("expected field 4 (status) = 3, got %+v (ok=%v)", status, ok)
	}

	envelope, ok := findField(fields, 5)
	if !ok {
		t.Fatalf("expected field 5 (metadata envelope) to be present")
	}
	envelopeFields := decodeProtoFields(t, envelope.Bytes)
	source, ok := findField(envelopeFields, 3)
	if !ok || source.Varint != 4 {
		t.Fatalf("expected envelope field 3 (source) = 4 (USER_EXPLICIT), got %+v (ok=%v)", source, ok)
	}

	body, ok := findField(fields, 19)
	if !ok {
		t.Fatalf("expected field 19 (content) to be present")
	}
	bodyFields := decodeProtoFields(t, body.Bytes)
	text, ok := findField(bodyFields, 2)
	if !ok || string(text.Bytes) != "hello from the user" {
		t.Fatalf("expected body field 2 = %q, got %+v (ok=%v)", "hello from the user", text, ok)
	}
}

func TestAntigravityModelStepPayload_ContentRoundTrips(t *testing.T) {
	when := time.Date(2026, 7, 20, 12, 0, 5, 0, time.UTC)
	payload := antigravityModelStepPayload("traj-1", "conv-1", 1, "hello from the model", when)

	fields := decodeProtoFields(t, payload)

	stepType, ok := findField(fields, 1)
	if !ok || stepType.Varint != 15 {
		t.Fatalf("expected field 1 (step_type) = 15, got %+v (ok=%v)", stepType, ok)
	}

	envelope, ok := findField(fields, 5)
	if !ok {
		t.Fatalf("expected field 5 (metadata envelope) to be present")
	}
	envelopeFields := decodeProtoFields(t, envelope.Bytes)
	source, ok := findField(envelopeFields, 3)
	if !ok || source.Varint != 2 {
		t.Fatalf("expected envelope field 3 (source) = 2 (MODEL), got %+v (ok=%v)", source, ok)
	}

	body, ok := findField(fields, 20)
	if !ok {
		t.Fatalf("expected field 20 (content) to be present")
	}
	bodyFields := decodeProtoFields(t, body.Bytes)
	text, ok := findField(bodyFields, 1)
	if !ok || string(text.Bytes) != "hello from the model" {
		t.Fatalf("expected body field 1 = %q, got %+v (ok=%v)", "hello from the model", text, ok)
	}
	botID, ok := findField(bodyFields, 6)
	if !ok || !strings.HasPrefix(string(botID.Bytes), "bot-") {
		t.Fatalf("expected body field 6 to start with 'bot-', got %+v (ok=%v)", botID, ok)
	}
}

func TestAntigravityStepPayloads_DifferentIndexesProduceDifferentBackrefs(t *testing.T) {
	when := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	p0 := antigravityUserStepPayload("traj-1", "conv-1", 0, "first", when)
	p1 := antigravityUserStepPayload("traj-1", "conv-1", 1, "second", when)

	envelope0, _ := findField(decodeProtoFields(t, p0), 5)
	envelope1, _ := findField(decodeProtoFields(t, p1), 5)
	backref0, _ := findField(decodeProtoFields(t, envelope0.Bytes), 20)
	backref1, _ := findField(decodeProtoFields(t, envelope1.Bytes), 20)

	idx0, _ := findField(decodeProtoFields(t, backref0.Bytes), 2)
	idx1, _ := findField(decodeProtoFields(t, backref1.Bytes), 2)
	if idx0.Varint != 0 {
		t.Errorf("expected first step's backref index = 0, got %d", idx0.Varint)
	}
	if idx1.Varint != 1 {
		t.Errorf("expected second step's backref index = 1, got %d", idx1.Varint)
	}
}
