package main

import (
	"encoding/json"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/df-mc/dragonfly/server/world"
	"github.com/df-mc/dragonfly/server/world/mcdb"
	"github.com/df-mc/goleveldb/leveldb/opt"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sirupsen/logrus"
)

var pre117 = true
var outputName = "world-out"

type mapGroup struct {
	worlds         map[string]*worldMap
	boundsTotal    protocol.ChunkPos
	offsetInOutput protocol.ChunkPos
}

type worldMap struct {
	*mcdb.Provider
	boundsMin     protocol.ChunkPos
	boundsMax     protocol.ChunkPos
	boundsTotal   protocol.ChunkPos
	offsetInGroup protocol.ChunkPos
}

type worldJson struct {
	Name   string
	Size   protocol.ChunkPos
	Offset protocol.ChunkPos
}

type groupJson struct {
	Worlds map[string]worldJson
}

type mapJson struct {
	Groups map[string]groupJson
}

func (w *worldMap) calcBounds() {
	for pos := range w.Provider.Chunks(pre117) {
		if w.boundsMin[0] > pos.P.X() {
			w.boundsMin[0] = pos.P.X()
		}
		if w.boundsMin[1] > pos.P.Z() {
			w.boundsMin[1] = pos.P.Z()
		}
		if w.boundsMax[0] < pos.P.X() {
			w.boundsMax[0] = pos.P.X()
		}
		if w.boundsMax[1] < pos.P.Z() {
			w.boundsMax[1] = pos.P.Z()
		}
	}

	w.boundsTotal = protocol.ChunkPos{
		w.boundsMax[0] - w.boundsMin[0] + 1,
		w.boundsMax[1] - w.boundsMin[1] + 1,
	}
	println()
}

func main() {
	worldPaths, err := filepath.Glob("inputs/**/*.mcworld")
	if err != nil {
		logrus.Fatal(err)
	}

	os.RemoveAll("tmp")
	os.RemoveAll(outputName)

	var worldGroups = map[string]mapGroup{}
	for _, v := range worldPaths {
		v = filepath.ToSlash(v)
		p := strings.Split(v, ".")
		parts := strings.Split(p[0], "/")[1:]
		groupName := parts[0]
		mapName := parts[1]

		s := path.Join("tmp", mapName)
		err := UnpackZip(v, s)
		if err != nil {
			logrus.Fatal(err)
		}

		prov, err := mcdb.New(logrus.StandardLogger(), s, opt.DefaultCompression)
		if err != nil {
			logrus.Fatal(err)
		}

		if _, ok := worldGroups[groupName]; !ok {
			worldGroups[groupName] = mapGroup{
				worlds: make(map[string]*worldMap),
			}
		}

		w := &worldMap{Provider: prov}
		w.calcBounds()
		worldGroups[groupName].worlds[mapName] = w
	}

	var maxGroupSize protocol.ChunkPos
	for _, group := range worldGroups {
		var maxWorldSize protocol.ChunkPos
		for _, world := range group.worlds {
			if world.boundsTotal.X() > maxWorldSize.X() {
				maxWorldSize[0] = world.boundsTotal.X()
			}
			if world.boundsTotal.Z() > maxWorldSize.Z() {
				maxWorldSize[1] = world.boundsTotal.Z()
			}
		}
		// 80 chunks of padding
		maxWorldSize[0] += 80
		maxWorldSize[1] += 80

		worldsSideLength := int(math.Ceil(math.Sqrt(float64(len(group.worlds)))))
		group.boundsTotal[0] = int32(worldsSideLength) * maxWorldSize.X()
		group.boundsTotal[1] = int32(worldsSideLength) * maxWorldSize.Z()

		i := 0
		for _, world := range group.worlds {
			row := i % worldsSideLength
			column := i / worldsSideLength
			world.offsetInGroup[0] = int32(column) * maxWorldSize.X()
			world.offsetInGroup[1] = int32(row) * maxWorldSize.Z()
			i += 1
		}

		if group.boundsTotal.X() > maxGroupSize.X() {
			maxGroupSize[0] = group.boundsTotal.X()
		}
		if group.boundsTotal.Z() > maxGroupSize.Z() {
			maxGroupSize[1] = group.boundsTotal.Z()
		}
	}

	groupsSideLength := int(math.Ceil(math.Sqrt(float64(len(worldGroups)))))
	i := 0
	for _, group := range worldGroups {
		row := i % groupsSideLength
		column := i / groupsSideLength
		group.offsetInOutput[0] = int32(column) * maxGroupSize.X()
		group.offsetInOutput[1] = int32(row) * maxGroupSize.Z()
		i += 1
	}

	providerOut, err := mcdb.New(logrus.StandardLogger(), outputName, opt.DefaultCompression)
	if err != nil {
		logrus.Fatal(err)
	}

	for _, group := range worldGroups {
		for _, w := range group.worlds {
			var outputOffset = protocol.ChunkPos{
				group.offsetInOutput.X() + w.offsetInGroup.X(),
				group.offsetInOutput.Z() + w.offsetInGroup.Z(),
			}

			for pos := range w.Provider.Chunks(pre117) {
				c, exists, err := w.Provider.LoadChunk(pos.P, pos.D)
				if err != nil {
					logrus.Fatal(err)
				}
				if !exists {
					panic("doesnt exist!!!")
				}

				blockNBT, err := w.Provider.LoadBlockNBT(pos.P, pos.D)
				if err != nil {
					logrus.Fatal(err)
				}

				for _, v := range blockNBT {
					v["x"] = v["x"].(int32) + outputOffset.X()*16
					v["z"] = v["z"].(int32) + outputOffset.Z()*16
				}

				entities, err := w.Provider.LoadEntities(pos.P, pos.D, *(*world.EntityRegistry)(unsafe.Pointer(&EntityRegistry{})))
				if err != nil {
					logrus.Fatal(err)
				}

				// TODO OFFSET ENTITIES

				var posOut = world.ChunkPos{
					outputOffset.X() + pos.P.X(),
					outputOffset.Z() + pos.P.Z(),
				}

				err = providerOut.SaveChunk(posOut, c, world.Overworld)
				if err != nil {
					logrus.Fatal(err)
				}

				err = providerOut.SaveBlockNBT(posOut, blockNBT, world.Overworld)
				if err != nil {
					logrus.Fatal(err)
				}

				err = providerOut.SaveEntities(posOut, entities, world.Overworld)
				if err != nil {
					logrus.Fatal(err)
				}
			}
		}
	}

	providerOut.SaveSettings(&world.Settings{
		Name: "world",
	})
	err = providerOut.Close()
	if err != nil {
		logrus.Fatal(err)
	}

	err = ZipFolder("world.mcworld", outputName)
	if err != nil {
		logrus.Fatal(err)
	}

	f, err := os.Create("map.json")
	if err != nil {
		logrus.Fatal(err)
	}

	m := mapJson{
		Groups: make(map[string]groupJson),
	}

	for k, mg := range worldGroups {
		m.Groups[k] = groupJson{
			Worlds: make(map[string]worldJson),
		}
		for k2, wm := range mg.worlds {
			m.Groups[k].Worlds[k2] = worldJson{
				Name: k2,
				Size: wm.boundsTotal,
				Offset: protocol.ChunkPos{
					mg.offsetInOutput[0] + wm.offsetInGroup[0],
					mg.offsetInOutput[1] + wm.offsetInGroup[1],
				},
			}
		}
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "\t")
	enc.Encode(m)
}
