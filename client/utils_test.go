// SPDX-FileCopyrightText: 2026 sb0-0dev
// SPDX-License-Identifier: GPL-2.0-or-later

package openrgb

import "testing"

func TestPacketDecoderRejectsTruncatedPayload(t *testing.T) {
	decoder := newPacketDecoder([]byte{1, 2, 3}, 0)
	if got := decoder.uint32(); got != 0 {
		t.Fatalf("truncated uint32 = %d, want zero value", got)
	}
	if decoder.Err() == nil {
		t.Fatal("expected truncated payload error")
	}
}

func TestColorConversionRoundTrip(t *testing.T) {
	colors := []RGBColor{{R: 0, G: 0, B: 0}, {R: 0x12, G: 0x34, B: 0x56}, {R: 255, G: 128, B: 1}}
	for _, want := range colors {
		if got := ConvertToRGBColor(ConvertRGBtoUint32(want)); got != want {
			t.Fatalf("round trip for %+v produced %+v", want, got)
		}
	}
}

func TestSegmentsCollectionMaintainsContiguousIndices(t *testing.T) {
	segments := SegmentsCollection{NumLeds: 10}
	if err := segments.AddItem("first", 3); err != nil {
		t.Fatal(err)
	}
	if err := segments.AddItem("second", 4); err != nil {
		t.Fatal(err)
	}
	if err := segments.UpdateItem(0, 2); err != nil {
		t.Fatal(err)
	}
	if got := segments.Segments[1].Segment.StartIdx; got != 2 {
		t.Fatalf("second segment starts at %d, want 2", got)
	}
	if err := segments.RemoveItem(0); err != nil {
		t.Fatal(err)
	}
	if got := segments.Segments[0].Segment.StartIdx; got != 0 {
		t.Fatalf("remaining segment starts at %d, want 0", got)
	}
	if got := segments.Segments[0].SegmentIdx; got != 0 {
		t.Fatalf("remaining segment index = %d, want 0", got)
	}
	if err := segments.AddItem("third", 2); err != nil {
		t.Fatal(err)
	}
	if got := segments.Segments[1].SegmentIdx; got != 1 {
		t.Fatalf("new segment index = %d, want 1", got)
	}
	if err := segments.UpdateItem(1, 7); err == nil {
		t.Fatal("UpdateItem accepted a segment size that exceeds the zone capacity")
	}
	if got := segments.Segments[1].Segment.NumLeds; got != 2 {
		t.Fatalf("rejected resize changed segment size to %d, want 2", got)
	}
	if err := segments.AddItem("too-large", 11); err == nil {
		t.Fatal("expected segment size error")
	}
}

func TestAddItemRejectsCapacityOverflow(t *testing.T) {
	segments := SegmentsCollection{
		NumLeds: ^uint32(0),
		Segments: []SegmentItem{{
			Segment: Segment{NumLeds: ^uint32(0)},
		}},
	}
	if err := segments.AddItem("overflow", 1); err == nil {
		t.Fatal("AddItem accepted a collection whose total LED count overflows uint32")
	}
}

func TestUtilityPredicatesAndDecoderByte(t *testing.T) {
	if !contains([]PacketType{PKT_REQUEST_PROFILE_LIST, PKT_REQUEST_SAVE_PROFILE}, PKT_REQUEST_SAVE_PROFILE) {
		t.Fatal("contains missed an existing packet type")
	}
	if contains([]PacketType{PKT_REQUEST_PROFILE_LIST}, PKT_REQUEST_SAVE_PROFILE) {
		t.Fatal("contains matched an absent packet type")
	}
	if !intToBool(1) || intToBool(0) {
		t.Fatal("intToBool returned incorrect values")
	}
	decoder := newPacketDecoder([]byte{0x7f}, 0)
	if got := decoder.Byte(); got != 0x7f || decoder.Err() != nil {
		t.Fatalf("Byte() = %d, error = %v", got, decoder.Err())
	}
	if got := newPacketDecoder(nil, 0).Byte(); got != 0 {
		t.Fatalf("truncated Byte() = %d, want 0", got)
	}
}
