package spancodec_test

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"github.com/apstndb/spantype/typector"
	"github.com/apstndb/spanvalue/gcvctor"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/apstndb/spancodec"
)

// uint32AsInt64 is the canonical use case: a built-in Go type the client's
// encodeValue does not support.
func uint32AsInt64(v uint32) (spanner.GenericColumnValue, error) {
	return gcvctor.Int64Value(int64(v)), nil
}

// uint64AsInt64 needs a range check: values above MaxInt64 have no INT64
// representation.
func uint64AsInt64(v uint64) (spanner.GenericColumnValue, error) {
	if v > math.MaxInt64 {
		return spanner.GenericColumnValue{}, fmt.Errorf("uint64 %d overflows INT64", v)
	}
	return gcvctor.Int64Value(int64(v)), nil
}

func TestWithValueEncoder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		v    any
		opts []spancodec.EncodeOption
		want spanner.GenericColumnValue
	}{
		{
			"uint32 scalar",
			uint32(7),
			[]spancodec.EncodeOption{spancodec.WithValueEncoder(uint32AsInt64)},
			spanner.GenericColumnValue{Type: typector.Int64(), Value: str("7")},
		},
		{
			"uint64 in range",
			uint64(8),
			[]spancodec.EncodeOption{spancodec.WithValueEncoder(uint64AsInt64)},
			spanner.GenericColumnValue{Type: typector.Int64(), Value: str("8")},
		},
		{
			"time.Duration as INT64 nanoseconds",
			2 * time.Second,
			[]spancodec.EncodeOption{spancodec.WithValueEncoder(func(d time.Duration) (spanner.GenericColumnValue, error) {
				return gcvctor.Int64Value(d.Nanoseconds()), nil
			})},
			spanner.GenericColumnValue{Type: typector.Int64(), Value: str(strconv.FormatInt(2e9, 10))},
		},
		{
			"overrides built-in string handling",
			"abc",
			[]spancodec.EncodeOption{spancodec.WithValueEncoder(func(s string) (spanner.GenericColumnValue, error) {
				return gcvctor.StringValue(strings.ToUpper(s)), nil
			})},
			spanner.GenericColumnValue{Type: typector.String(), Value: str("ABC")},
		},
		{
			"overrides a spanner.Encoder implementation",
			myEncoder{v: "ignored"},
			[]spancodec.EncodeOption{spancodec.WithValueEncoder(func(myEncoder) (spanner.GenericColumnValue, error) {
				return gcvctor.StringValue("registered"), nil
			})},
			spanner.GenericColumnValue{Type: typector.String(), Value: str("registered")},
		},
		{
			"overrides the named-type conversion",
			myString("x"),
			[]spancodec.EncodeOption{spancodec.WithValueEncoder(func(myString) (spanner.GenericColumnValue, error) {
				return gcvctor.StringValue("custom"), nil
			})},
			spanner.GenericColumnValue{Type: typector.String(), Value: str("custom")},
		},
		{
			// Pins the documented hazard: a time.Time registration bypasses
			// the CommitTimestamp sentinel detection of the mirror.
			"time.Time registration bypasses CommitTimestamp sentinel",
			spanner.CommitTimestamp,
			[]spancodec.EncodeOption{spancodec.WithValueEncoder(func(time.Time) (spanner.GenericColumnValue, error) {
				return gcvctor.StringValue("intercepted"), nil
			})},
			spanner.GenericColumnValue{Type: typector.String(), Value: str("intercepted")},
		},
		{
			"last registration wins",
			uint32(1),
			[]spancodec.EncodeOption{
				spancodec.WithValueEncoder(func(uint32) (spanner.GenericColumnValue, error) {
					return gcvctor.StringValue("first"), nil
				}),
				spancodec.WithValueEncoder(uint32AsInt64),
			},
			spanner.GenericColumnValue{Type: typector.Int64(), Value: str("1")},
		},
		{
			"fallthrough defers to the mirror",
			"plain",
			[]spancodec.EncodeOption{spancodec.WithValueEncoder(func(s string) (spanner.GenericColumnValue, error) {
				if s == "special" {
					return gcvctor.StringValue("SPECIAL"), nil
				}
				return spanner.GenericColumnValue{}, spancodec.ErrFallthrough
			})},
			spanner.GenericColumnValue{Type: typector.String(), Value: str("plain")},
		},
		{
			"slice of registered type encodes per element",
			[]uint32{1, 2},
			[]spancodec.EncodeOption{spancodec.WithValueEncoder(uint32AsInt64)},
			spanner.GenericColumnValue{Type: typector.ElemCodeToArrayType(sppb.TypeCode_INT64), Value: listValue(str("1"), str("2"))},
		},
		{
			"slice elements honor registration over the built-in case",
			[]string{"a"},
			[]spancodec.EncodeOption{spancodec.WithValueEncoder(func(s string) (spanner.GenericColumnValue, error) {
				return gcvctor.StringValue(strings.ToUpper(s)), nil
			})},
			spanner.GenericColumnValue{Type: typector.ElemCodeToArrayType(sppb.TypeCode_STRING), Value: listValue(str("A"))},
		},
		{
			"nil slice with WithGoType is a typed NULL ARRAY",
			[]uint32(nil),
			[]spancodec.EncodeOption{
				spancodec.WithValueEncoder(uint32AsInt64),
				spancodec.WithGoType[uint32](typector.Int64()),
			},
			spanner.GenericColumnValue{Type: typector.ElemCodeToArrayType(sppb.TypeCode_INT64), Value: nullValue()},
		},
		{
			"empty slice with WithGoType is an empty ARRAY",
			[]uint32{},
			[]spancodec.EncodeOption{
				spancodec.WithValueEncoder(uint32AsInt64),
				spancodec.WithGoType[uint32](typector.Int64()),
			},
			spanner.GenericColumnValue{Type: typector.ElemCodeToArrayType(sppb.TypeCode_INT64), Value: listValue()},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := spancodec.ValueOf(tt.v, tt.opts...)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("ValueOf mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWithValueEncoderErrors(t *testing.T) {
	t.Parallel()

	t.Run("encoder error propagates", func(t *testing.T) {
		t.Parallel()
		_, err := spancodec.ValueOf(uint64(math.MaxUint64), spancodec.WithValueEncoder(uint64AsInt64))
		if err == nil || !strings.Contains(err.Error(), "overflows INT64") {
			t.Errorf("error = %v, want overflow error", err)
		}
	})

	t.Run("struct field error is wrapped with the field", func(t *testing.T) {
		t.Parallel()
		type row struct {
			N uint64 `spanner:"n"`
		}
		_, _, err := spancodec.StructColumnsAndValues(row{N: math.MaxUint64}, spancodec.WithValueEncoder(uint64AsInt64))
		var sfe *gcvctor.StructFieldError
		if !errors.As(err, &sfe) || sfe.Name != "n" {
			t.Errorf("error = %v, want StructFieldError for field n", err)
		}
	})

	t.Run("nil slice without WithGoType defers to the mirror", func(t *testing.T) {
		t.Parallel()
		if _, err := spancodec.ValueOf([]uint32(nil), spancodec.WithValueEncoder(uint32AsInt64)); !errors.Is(err, spancodec.ErrUnsupportedType) {
			t.Errorf("error = %v, want ErrUnsupportedType", err)
		}
		// Built-in element types keep their mirror behavior even when the
		// element encoder always falls through.
		got, err := spancodec.ValueOf([]string(nil), spancodec.WithValueEncoder(func(string) (spanner.GenericColumnValue, error) {
			return spanner.GenericColumnValue{}, spancodec.ErrFallthrough
		}))
		if err != nil {
			t.Fatal(err)
		}
		want := spanner.GenericColumnValue{Type: typector.ElemCodeToArrayType(sppb.TypeCode_STRING), Value: nullValue()}
		if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
			t.Errorf("nil []string mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("slice element error is wrapped with the index", func(t *testing.T) {
		t.Parallel()
		_, err := spancodec.ValueOf([]uint64{1, math.MaxUint64}, spancodec.WithValueEncoder(uint64AsInt64))
		var aee *gcvctor.ArrayElementError
		if !errors.As(err, &aee) || aee.Index != 1 {
			t.Errorf("error = %v, want ArrayElementError at index 1", err)
		}
	})

	t.Run("interface type registration panics", func(t *testing.T) {
		t.Parallel()
		defer func() {
			if recover() == nil {
				t.Error("WithValueEncoder[error]: want panic, got none")
			}
		}()
		_, _ = spancodec.ValueOf("x", spancodec.WithValueEncoder(func(error) (spanner.GenericColumnValue, error) {
			return spanner.GenericColumnValue{}, nil
		}))
	})
}

// TestSliceHelpersWithGoType pins that ValuesFromSlice / ArrayValueFromSlice
// accept WithGoType + WithValueEncoder for element types that static
// inference alone rejects.
func TestSliceHelpersWithGoType(t *testing.T) {
	t.Parallel()

	opts := []spancodec.EncodeOption{
		spancodec.WithValueEncoder(uint32AsInt64),
		spancodec.WithGoType[uint32](typector.Int64()),
	}

	elemType, values, err := spancodec.ValuesFromSlice([]uint32{1, 2}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(typector.Int64(), elemType, protocmp.Transform()); diff != "" {
		t.Errorf("element type mismatch (-want +got):\n%s", diff)
	}
	if len(values) != 2 {
		t.Errorf("values = %v, want 2 elements", values)
	}

	got, err := spancodec.ArrayValueFromSlice([]uint32(nil), opts...)
	if err != nil {
		t.Fatal(err)
	}
	want := spanner.GenericColumnValue{Type: typector.ElemCodeToArrayType(sppb.TypeCode_INT64), Value: nullValue()}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("nil slice mismatch (-want +got):\n%s", diff)
	}

	// Without the options, static inference still rejects uint32.
	if _, _, err := spancodec.ValuesFromSlice([]uint32{1}); !errors.Is(err, spancodec.ErrUnsupportedType) {
		t.Errorf("error = %v, want ErrUnsupportedType", err)
	}
}

// TestRowEncoderConstructionOptions pins the RowEncoderOption union: both
// option families configure NewRowEncoder, construction-time registrations
// inform RowType/ResultSetMetadata, and per-call options never leak into
// the shared encoder state.
func TestRowEncoderConstructionOptions(t *testing.T) {
	t.Parallel()

	type row struct {
		Name   string `spanner:"name"`
		Count  uint32 `spanner:"count"`
		Hidden string `spanner:"hidden"`
	}
	enc, err := spancodec.NewRowEncoder[row](
		spancodec.WithoutColumns("hidden"),
		spancodec.WithValueEncoder(uint32AsInt64),
		spancodec.WithGoType[uint32](typector.Int64()),
	)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"name", "count"}, enc.Columns()); diff != "" {
		t.Errorf("Columns mismatch (-want +got):\n%s", diff)
	}

	rowType, err := enc.RowType()
	if err != nil {
		t.Fatal(err)
	}
	wantRowType := &sppb.StructType{Fields: []*sppb.StructType_Field{
		typector.NameCodeToStructTypeField("name", sppb.TypeCode_STRING),
		typector.NameCodeToStructTypeField("count", sppb.TypeCode_INT64),
	}}
	if diff := cmp.Diff(wantRowType, rowType, protocmp.Transform()); diff != "" {
		t.Errorf("RowType mismatch (-want +got):\n%s", diff)
	}

	values, err := enc.Values(row{Name: "a", Count: 7, Hidden: "x"})
	if err != nil {
		t.Fatal(err)
	}
	want := []spanner.GenericColumnValue{
		{Type: typector.String(), Value: str("a")},
		{Type: typector.Int64(), Value: str("7")},
	}
	if diff := cmp.Diff(want, values, protocmp.Transform()); diff != "" {
		t.Errorf("Values mismatch (-want +got):\n%s", diff)
	}

	// Per-call options layer on top without mutating the encoder.
	override, err := enc.Values(row{Name: "a", Count: 7},
		spancodec.WithValueEncoder(func(v uint32) (spanner.GenericColumnValue, error) {
			return gcvctor.StringValue("overridden"), nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	if got := override[1].Value.GetStringValue(); got != "overridden" {
		t.Errorf("per-call override = %q, want %q", got, "overridden")
	}
	again, err := enc.Values(row{Name: "a", Count: 7})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want[1], again[1], protocmp.Transform()); diff != "" {
		t.Errorf("per-call options leaked into encoder state (-want +got):\n%s", diff)
	}
}

// TestTypeInferenceWithGoType pins WithGoType resolution in the static
// inference entry points.
func TestTypeInferenceWithGoType(t *testing.T) {
	t.Parallel()

	opt := spancodec.WithGoType[uint32](typector.Int64())

	got, err := spancodec.TypeFor[uint32](opt)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(typector.Int64(), got, protocmp.Transform()); diff != "" {
		t.Errorf("TypeFor mismatch (-want +got):\n%s", diff)
	}

	gotArr, err := spancodec.TypeFor[[]uint32](opt)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(typector.ElemCodeToArrayType(sppb.TypeCode_INT64), gotArr, protocmp.Transform()); diff != "" {
		t.Errorf("TypeFor slice mismatch (-want +got):\n%s", diff)
	}

	type row struct {
		Count uint32 `spanner:"count"`
	}
	rowType, err := spancodec.RowTypeFor[row](opt)
	if err != nil {
		t.Fatal(err)
	}
	wantRowType := &sppb.StructType{Fields: []*sppb.StructType_Field{
		typector.NameCodeToStructTypeField("count", sppb.TypeCode_INT64),
	}}
	if diff := cmp.Diff(wantRowType, rowType, protocmp.Transform()); diff != "" {
		t.Errorf("RowTypeFor mismatch (-want +got):\n%s", diff)
	}

	md, err := spancodec.ResultSetMetadataFor[row](opt)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(wantRowType, md.GetRowType(), protocmp.Transform()); diff != "" {
		t.Errorf("ResultSetMetadataFor mismatch (-want +got):\n%s", diff)
	}

	// Without the option the inference still rejects uint32.
	if _, err := spancodec.TypeFor[uint32](); !errors.Is(err, spancodec.ErrUnsupportedType) {
		t.Errorf("error = %v, want ErrUnsupportedType", err)
	}
}

// TestSliceElementInferencePrecedence pins that a registered element type
// resolves []T even when the slice type itself is client-native (found by
// spanpg, apstndb/spanpg#5): inference precedence matches encodeValue —
// exact []T registration > element registration > client mirror.
func TestSliceElementInferencePrecedence(t *testing.T) {
	t.Parallel()

	elemOpt := spancodec.WithGoType[big.Rat](typector.PGNumeric())

	got, err := spancodec.TypeFor[[]big.Rat](elemOpt)
	if err != nil {
		t.Fatal(err)
	}
	want := typector.ElemTypeToArrayType(typector.PGNumeric())
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("element registration mismatch (-want +got):\n%s", diff)
	}

	// An exact registration for the slice type itself still wins.
	exact := typector.ElemCodeToArrayType(sppb.TypeCode_STRING)
	gotExact, err := spancodec.TypeFor[[]big.Rat](elemOpt, spancodec.WithGoType[[]big.Rat](exact))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(exact, gotExact, protocmp.Transform()); diff != "" {
		t.Errorf("exact-slice registration mismatch (-want +got):\n%s", diff)
	}

	// Struct fields resolve through the same precedence (RowEncoder/RowTypeFor).
	type row struct {
		Ns []big.Rat `spanner:"ns"`
	}
	rowType, err := spancodec.RowTypeFor[row](elemOpt)
	if err != nil {
		t.Fatal(err)
	}
	wantRowType := &sppb.StructType{Fields: []*sppb.StructType_Field{
		typector.NameTypeToStructTypeField("ns", typector.ElemTypeToArrayType(typector.PGNumeric())),
	}}
	if diff := cmp.Diff(wantRowType, rowType, protocmp.Transform()); diff != "" {
		t.Errorf("RowTypeFor mismatch (-want +got):\n%s", diff)
	}

	// Without options the client-native inference is unchanged.
	plain, err := spancodec.TypeFor[[]big.Rat]()
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(typector.ElemCodeToArrayType(sppb.TypeCode_NUMERIC), plain, protocmp.Transform()); diff != "" {
		t.Errorf("no-options inference changed (-want +got):\n%s", diff)
	}
}
