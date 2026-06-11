# spancodec

[![Go Reference](https://pkg.go.dev/badge/github.com/apstndb/spancodec.svg)](https://pkg.go.dev/github.com/apstndb/spancodec)

Convert between plain Go values and Cloud Spanner
[`GenericColumnValue`](https://pkg.go.dev/cloud.google.com/go/spanner#GenericColumnValue)s
(GCVs), following the official client library's own semantics in both
directions. This module merges and supersedes
[spanenc](https://github.com/apstndb/spanenc) and
[spandec](https://github.com/apstndb/spandec).

- **Encoding mirrors the client** — the internal `encodeValue` type coverage,
  `spanner` struct tag rules (including `;`-separated options and
  read-only markers), typed NULLs, `spanner.Null*` wrappers,
  `spanner.Encoder`, protobuf messages/enums, and
  `spanner.CommitTimestamp`. Mirrored semantics track
  `cloud.google.com/go/spanner` v1.91.0.
- **Decoding delegates to the client** — `Decode` / `ToStruct` behave
  exactly like `GenericColumnValue.Decode` / `Row.ToStruct`, extended only
  for destination shapes the client rejects today (pointers to named
  scalars, `[]T` struct slices, `json.RawMessage`), each tied to an
  upstream issue.
- **Custom value codecs** — per-call `WithValueEncoder` / `WithValueDecoder`
  injection for Go types outside the client's coverage (`uint32` and other
  integer width variants, `time.Duration`, external types), with
  `ErrFallthrough` deferral to the built-in handling.
- **Row tooling** — `StructColumns`, `RowTypeFor`, `ResultSetMetadataFor`,
  compiled `RowEncoder[T]` (`Columns` / `RowType` / `Values` / `Row` /
  `Rows`), mutation/params helpers (`MutationColumnsAndValues`,
  `MutationMap`, `ParamsMap`) with include/exclude column masks.

All guidance lives in the
[package documentation](https://pkg.go.dev/github.com/apstndb/spancodec)
(sections: API overview, Struct field listings, Divergences, Decoding,
Custom value codecs, Adoption guide) and its runnable examples. Minimum
dependency versions are recorded in the
[release notes](https://github.com/apstndb/spancodec/releases) of each
version.

## Migrating from spanenc / spandec

Import path changes aside, the API is the same with two intentional breaks
relative to spanenc v0.3.x:

| spanenc/spandec | spancodec |
|---|---|
| `spanenc.NewRowEncoder[T](opts ...ColumnMaskOption)` | `NewRowEncoder[T](opts ...RowEncoderOption)` — accepts both `ColumnMaskOption` and `EncodeOption`; existing call sites compile unchanged unless they spread a `[]ColumnMaskOption` |
| `spanenc.TypeFor[T]()` / `TypeFromGoType(t)` / `RowTypeFor[T]()` / `RowTypeFromGoType(t)` / `ResultSetMetadataFor[T]()` / `ResultSetMetadataFromGoType(t)` | same names with `opts ...EncodeOption` — `WithGoType` registrations now resolve static inference |
| `spanenc.ErrFallthrough` / `spandec.ErrFallthrough` | one `ErrFallthrough` for both directions |
| `spandec.Decode` / `spandec.ToStruct` | unchanged signatures (already variadic) |

## License

MIT. Behavioral mirroring of `cloud.google.com/go/spanner` uses independent
expression; upstream-derived code lives in the Apache-2.0
[structfields](https://github.com/apstndb/structfields) module instead.
