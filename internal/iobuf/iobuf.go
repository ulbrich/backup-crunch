// Package iobuf holds the shared streaming-copy buffer size used by the copier
// and hasher, so content is moved in fixed-size chunks and never fully buffered
// in memory regardless of file size.
package iobuf

// Size is the streaming buffer size (1 MiB).
const Size = 1 << 20
