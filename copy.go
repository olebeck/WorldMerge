package main

import (
	"encoding/binary"
	"fmt"

	"github.com/df-mc/dragonfly/server/world"
	"github.com/df-mc/dragonfly/server/world/mcdb"
	"github.com/df-mc/goleveldb/leveldb"
)

// Keys on a per-sub chunk basis. These are prefixed by the chunk coordinates and subchunk ID.
const (
	keySubChunkData = '/' // 2f
)

// Keys on a per-chunk basis. These are prefixed by only the chunk coordinates.
const (
	// keyVersion holds a single byte of data with the version of the chunk.
	keyVersion = ',' // 2c
	// keyVersionOld was replaced by keyVersion. It is still used by vanilla to check compatibility, but vanilla no
	// longer writes this tag.
	keyVersionOld = 'v' // 76
	// key3DData holds 3-dimensional biomes for the entire chunk.
	key3DData = '+' // 2b
)

func key_index(position world.ChunkPos, d world.Dimension) []byte {
	dim, _ := world.DimensionID(d)
	x, z := uint32(position[0]), uint32(position[1])
	b := make([]byte, 12)

	binary.LittleEndian.PutUint32(b, x)
	binary.LittleEndian.PutUint32(b[4:], z)
	if dim == 0 {
		return b[:8]
	}
	binary.LittleEndian.PutUint32(b[8:], uint32(dim))
	return b
}

const chunkVersion = 40

var countChunks = 0

func copyChunk(db *mcdb.DB, pos world.ChunkPos, dim world.Dimension, dbOutput *leveldb.Batch, posOut world.ChunkPos) error {
	countChunks += 1
	keyi := key_index(pos, dim)
	keyo := key_index(posOut, dim)

	dbOutput.Put(append(keyo, keyVersion), []byte{chunkVersion})

	Biomes, err := db.LDB().Get(append(keyi, key3DData), nil)
	if err != nil && err != leveldb.ErrNotFound {
		return fmt.Errorf("error reading 3D data: %w", err)
	}
	if len(Biomes) > 512 {
		Biomes = Biomes[512:]
	}
	dbOutput.Put(append(keyo, key3DData), Biomes)

	for i := 0; i < (dim.Range().Height()>>4)+1; i++ {
		o := uint8(i + (dim.Range()[0] >> 4))
		SubChunk, err := db.LDB().Get(append(keyi, keySubChunkData, o), nil)
		if err == leveldb.ErrNotFound {
			// No sub chunk present at this Y level. We skip this one and move to the next, which might still
			// be present.
			continue
		} else if err != nil {
			return fmt.Errorf("error reading sub chunk data %v: %w", i, err)
		}
		dbOutput.Put(append(keyo, keySubChunkData, o), SubChunk)
	}
	return nil
}
