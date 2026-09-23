package spancodec_test

import (
	"encoding/json"
	"errors"
	"testing"

	"cloud.google.com/go/spanner"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"github.com/apstndb/spancodec"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/types/known/structpb"
)

func nilDestinationFixtures() (spanner.GenericColumnValue, spanner.GenericColumnValue) {
	return spanner.GenericColumnValue{
			Type:  &sppb.Type{Code: sppb.TypeCode_JSON},
			Value: structpb.NewStringValue(`{"n":1}`),
		}, spanner.GenericColumnValue{
			Type:  &sppb.Type{Code: sppb.TypeCode_ARRAY, ArrayElementType: &sppb.Type{Code: sppb.TypeCode_INT64}},
			Value: structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{structpb.NewStringValue("42")}}),
		}
}

func TestDecodeNilDestinations(t *testing.T) {
	t.Parallel()
	jsonValue, arrayValue := nilDestinationFixtures()
	for _, tc := range []struct {
		name  string
		value spanner.GenericColumnValue
		dst   any
		opts  []spancodec.DecodeOption
	}{
		{name: "JSON", value: jsonValue, dst: (*json.RawMessage)(nil)},
		{name: "ARRAY", value: arrayValue, dst: (*[]int64)(nil)},
		{name: "registered ARRAY", value: arrayValue, dst: (*[]int64)(nil), opts: []spancodec.DecodeOption{
			spancodec.WithValueDecoder(func(spanner.GenericColumnValue, *int64) error {
				return errors.New("element decoder must not run for a nil slice destination")
			}),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, null := range []bool{false, true} {
				name := "value"
				value := tc.value
				if null {
					name = "NULL"
					value.Value = structpb.NewNullValue()
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					want := value.Decode(tc.dst)
					if want == nil {
						t.Fatal("client accepted a nil destination")
					}
					got := spancodec.Decode(value, tc.dst, tc.opts...)
					if got == nil || got.Error() != want.Error() {
						t.Errorf("Decode() error = %v, want client error %v", got, want)
					}
				})
			}
		})
	}
}

func TestWithValueDecoderNilDestination(t *testing.T) {
	t.Parallel()
	jsonValue, arrayValue := nilDestinationFixtures()
	t.Run("JSON", func(t *testing.T) { testNilDecoderPriority[json.RawMessage](t, jsonValue) })
	t.Run("ARRAY", func(t *testing.T) { testNilDecoderPriority[[]int64](t, arrayValue) })
}

func testNilDecoderPriority[T any](t *testing.T, value spanner.GenericColumnValue) {
	t.Helper()
	t.Parallel()
	for _, null := range []bool{false, true} {
		name := "value"
		value := value
		if null {
			name = "NULL"
			value.Value = structpb.NewNullValue()
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name   string
				result error
			}{
				{name: "override", result: errors.New("registered decoder wins")},
				{name: "fallthrough", result: spancodec.ErrFallthrough},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					called := false
					var dst *T
					got := spancodec.Decode(value, dst,
						spancodec.WithValueDecoder(func(spanner.GenericColumnValue, *int64) error {
							return errors.New("exact destination registration must take priority")
						}),
						spancodec.WithValueDecoder(func(_ spanner.GenericColumnValue, got *T) error {
							called = true
							if got != nil {
								t.Error("registered decoder received a non-nil destination")
							}
							return tc.result
						}),
					)
					if !called {
						t.Fatal("exact destination registration was skipped")
					}
					if !errors.Is(tc.result, spancodec.ErrFallthrough) {
						if !errors.Is(got, tc.result) {
							t.Errorf("Decode() error = %v, want registered error %v", got, tc.result)
						}
						return
					}
					want := value.Decode(dst)
					if want == nil {
						t.Fatal("client accepted a nil destination")
					}
					if got == nil || got.Error() != want.Error() {
						t.Errorf("Decode() error = %v, want client error %v", got, want)
					}
				})
			}
		})
	}
}

func TestWithValueDecoderSliceFallthrough(t *testing.T) {
	t.Parallel()
	_, arrayValue := nilDestinationFixtures()
	for _, tc := range []struct {
		name string
		wire *structpb.Value
		want []int64
	}{
		{name: "value", wire: arrayValue.Value, want: []int64{42}},
		{name: "empty", wire: structpb.NewListValue(&structpb.ListValue{}), want: []int64{}},
		{name: "NULL", wire: structpb.NewNullValue(), want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			value := arrayValue
			value.Value = tc.wire
			var got []int64
			if err := spancodec.Decode(value, &got, spancodec.WithValueDecoder(func(spanner.GenericColumnValue, *int64) error {
				return spancodec.ErrFallthrough
			})); err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Decode() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
