package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/df-mc/dragonfly/server/world"
	"github.com/df-mc/dragonfly/server/world/mcdb"
	"github.com/df-mc/goleveldb/leveldb"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"
)

const padding = 80

var sem = semaphore.NewWeighted(30)

type layoutItem interface {
	BoundsTotal() ChunkPos
	setOffset(ChunkPos)
}

type mapGroup struct {
	Name   string
	worlds map[string]*worldMap
	groups map[string]*mapGroup

	numCols, colWidth int32
	rowHeights        []int32

	offsetFromParent ChunkPos
}

func (g *mapGroup) BoundsTotal() ChunkPos {
	var z int32
	for _, v := range g.rowHeights {
		z += v
	}
	return ChunkPos{
		int32(g.numCols) * g.colWidth,
		z,
	}
}

func (g *mapGroup) setOffset(p ChunkPos) {
	g.offsetFromParent = p
}

type worldMap struct {
	Name             string
	filepath         string
	boundsMin        ChunkPos
	boundsMax        ChunkPos
	offsetFromParent ChunkPos
}

type worldJson struct {
	Name           string
	Size           ChunkPos
	OffsetAbsolute ChunkPos
}

type groupJson struct {
	Name   string
	Worlds map[string]worldJson
	Groups map[string]groupJson

	Size           ChunkPos
	OffsetAbsolute ChunkPos
}

type mapJson struct {
	Groups map[string]groupJson
}

func (w *worldMap) BoundsTotal() ChunkPos {
	return ChunkPos{
		w.boundsMax[0] - w.boundsMin[0] + 1,
		w.boundsMax[1] - w.boundsMin[1] + 1,
	}
}

func (w *worldMap) setOffset(p ChunkPos) {
	w.offsetFromParent = p
}

func (w *worldMap) calcBounds(db *mcdb.DB) {
	it := newChunkIterator(db, nil)
	for it.Next() {
		pos := it.Position()
		if w.boundsMin[0] > pos.X() {
			w.boundsMin[0] = pos.X()
		}
		if w.boundsMin[1] > pos.Z() {
			w.boundsMin[1] = pos.Z()
		}
		if w.boundsMax[0] < pos.X() {
			w.boundsMax[0] = pos.X()
		}
		if w.boundsMax[1] < pos.Z() {
			w.boundsMax[1] = pos.Z()
		}
	}
	it.Release()
}

var worldsAdded = 0
var worldsTotal = 0

func addWorlds(wg *sync.WaitGroup, dbOutput *mcdb.DB, baseOffset ChunkPos, worlds map[string]*worldMap) error {
	for _, w := range worlds {
		logrus.Infof("Adding %s %d/%d", w.Name, worldsAdded, worldsTotal)
		worldsAdded++

		wg.Add(1)
		sem.Acquire(context.Background(), 1)
		go func(w *worldMap) {
			defer wg.Done()
			defer sem.Release(1)
			var outputOffset = baseOffset.Add(w.offsetFromParent)

			db, err := mcdb.New(w.filepath)
			if err != nil {
				logrus.Error(err)
				return
			}

			it := newChunkIterator(db, nil)
			defer it.Release()
			for it.Next() {
			}

			b := leveldb.MakeBatch(len(it.seen) * 16)
			for k := range it.seen {
				pos := k.pos
				dim := k.dim
				var posOut = (world.ChunkPos)(outputOffset.Add(pos))

				err := copyChunk(db, pos, dim, b, posOut)
				if err != nil {
					logrus.Error(err)
					return
				}

				blockNBT, err := db.LoadBlockNBT(pos, dim)
				if err != nil {
					logrus.Error(err)
					return
				}
				for _, v := range blockNBT {
					x, ok := v["x"].(int32)
					if !ok {
						continue
					}
					z, ok := v["z"].(int32)
					if !ok {
						continue
					}
					v["x"] = x + outputOffset.X()*16
					v["z"] = z + outputOffset.Z()*16
				}
				err = dbOutput.SaveBlockNBT(posOut, blockNBT, world.Overworld)
				if err != nil {
					logrus.Error(err)
					return
				}

				entities, err := db.LoadEntities(pos, dim, &EntityRegistry{})
				if err != nil {
					logrus.Error(err)
					return
				}
				for _, e := range entities {
					ent := e.(*DummyEntity)
					entt := ent.T.(*DummyEntityType)
					entt.NBT["Pos"].([]any)[0] = entt.NBT["Pos"].([]any)[0].(float32) + float32(outputOffset.X()*16)
					entt.NBT["Pos"].([]any)[2] = entt.NBT["Pos"].([]any)[2].(float32) + float32(outputOffset.Z()*16)
				}
				err = dbOutput.SaveEntities(posOut, entities, world.Overworld)
				if err != nil {
					logrus.Error(err)
					return
				}
			}

			err = dbOutput.LDB().Write(b, nil)
			if err != nil {
				logrus.Error(err)
				return
			}
		}(w)
	}
	return nil
}

func addGroups(wg *sync.WaitGroup, dbOutput *mcdb.DB, baseOffset ChunkPos, groups map[string]*mapGroup) error {
	for _, group := range groups {
		if len(group.groups) > 0 {
			err := addGroups(wg, dbOutput, baseOffset.Add(group.offsetFromParent), group.groups)
			if err != nil {
				return err
			}
		}
		err := addWorlds(wg, dbOutput, baseOffset.Add(group.offsetFromParent), group.worlds)
		if err != nil {
			return err
		}
	}
	return nil
}

func recursiveAddWorld(filepath string, parts []string, groups map[string]*mapGroup) error {
	groupName := parts[0]
	group, ok := groups[groupName]
	if !ok {
		group = &mapGroup{
			Name:   groupName,
			worlds: make(map[string]*worldMap),
			groups: make(map[string]*mapGroup),
		}
		groups[groupName] = group
	}
	parts = parts[1:]
	if len(parts) == 1 {
		worldName := parts[0]

		stat, err := os.Stat(filepath)
		if err != nil {
			return err
		}
		if stat.IsDir() {
			logrus.Warnf("Empty Folder %s", filepath)
			return nil
		}

		if path.Ext(filepath) == ".mcworld" {
			s := path.Join("tmp", filepath)
			if _, err := os.Stat(s); err != nil {
				os.MkdirAll(s, 0755)
				err := UnpackZip(filepath, s, func(s string) bool {
					is_behaviors := strings.Contains(s, "behavior_packs")
					is_resources := strings.Contains(s, "resource_packs")
					return !is_resources && !is_behaviors
				})
				if err != nil {
					return err
				}
			}
			filepath = s
		} else {
			return fmt.Errorf("%s is not mcworld", filepath)
		}

		logrus.Infof("Getting Bounds %s", filepath)
		db, err := mcdb.New(filepath)
		if err != nil {
			logrus.Error(err)
			return nil
		}
		w := &worldMap{Name: worldName, filepath: filepath}
		w.calcBounds(db)
		db.Close()
		group.worlds[worldName] = w
		worldsTotal++
		return nil
	}
	return recursiveAddWorld(filepath, parts, group.groups)
}

func layoutWorld(w *worldMap, offset ChunkPos) {
	w.offsetFromParent = offset
}

func layoutGroup(g *mapGroup, parentOffset ChunkPos, padding int32) {
	// First, layout the children
	var children []layoutItem
	for _, childGroup := range g.groups {
		layoutGroup(childGroup, parentOffset, padding)
		children = append(children, childGroup)
	}
	for _, childWorld := range g.worlds {
		layoutWorld(childWorld, parentOffset)
		children = append(children, childWorld)
	}

	// Then, calculate the size and position of this group based on its children
	var maxWidth, maxHeight int32
	for _, child := range children {
		cb := child.BoundsTotal()
		if cb[0] > maxWidth {
			maxWidth = cb[0]
		}
		if cb[1] > maxHeight {
			maxHeight = cb[1]
		}
	}

	a := math.Sqrt(float64(len(g.groups) + len(g.worlds)))

	g.numCols = int32(math.Ceil(a))
	if g.numCols == 0 {
		g.numCols = 1
	}
	rows := int32(math.Floor(a))
	_ = rows
	g.colWidth = int32(maxWidth)
	if a > 0 {
		g.colWidth += padding
	}

	currentOffset := ChunkPos(parentOffset)
	var row, col int32
	var rowHeight int32 = maxHeight
	for _, child := range children {
		_, is_world := child.(*worldMap)
		cb := child.BoundsTotal()
		child.setOffset(ChunkPos{
			currentOffset.X() + col*g.colWidth,
			currentOffset.Z(),
		})

		childHeight := cb.Z()
		if is_world {
			childHeight += padding
		}

		col++
		if col == g.numCols {
			col = 0
			row++
			currentOffset[1] += rowHeight
			if is_world {
				currentOffset[1] += padding
			}
			currentOffset[0] = parentOffset[0]
			g.rowHeights = append(g.rowHeights, rowHeight)
		} else {
			if is_world {
				currentOffset[0] += padding
			}
		}
	}
}

func main() {
	if len(os.Args) < 2 {
		logrus.Error("Usage: WorldMerge.exe <input folder> [output-name]")
		return
	}
	inputFolder := os.Args[1]

	outputName := "world-out"
	if len(os.Args) >= 3 {
		outputName = os.Args[2]
	}
	os.RemoveAll(outputName)

	worldPaths, err := glob(inputFolder, ".mcworld")
	if err != nil {
		logrus.Fatal(err)
	}
	sort.Strings(worldPaths)

	// load
	var worldGroups = map[string]*mapGroup{}
	for _, v := range worldPaths {
		v = filepath.ToSlash(v)
		p := strings.Split(v, ".")
		parts := strings.Split(p[0], "/")[1:]
		err = recursiveAddWorld(v, parts, worldGroups)
		if err != nil {
			logrus.Fatal(err)
		}
	}

	logrus.Info("Laying Out")
	root := &mapGroup{groups: worldGroups}
	layoutGroup(root, ChunkPos{}, padding)

	// center root
	root.offsetFromParent = root.offsetFromParent.Sub(root.BoundsTotal().Div(2))

	err = writeGroupToJSON(root, "map.json")
	if err != nil {
		logrus.Fatal(err)
	}

	logrus.Info("Generating Output World")
	{ // output world
		providerOut, err := mcdb.New(outputName)
		if err != nil {
			logrus.Fatal(err)
		}

		wg := &sync.WaitGroup{}
		err = addGroups(wg, providerOut, ChunkPos{}, worldGroups)
		if err != nil {
			logrus.Fatal(err)
		}
		wg.Wait()

		providerOut.SaveSettings(&world.Settings{
			Name:            "world",
			DefaultGameMode: world.GameModeCreative,
		})
		err = providerOut.Close()
		if err != nil {
			logrus.Fatal(err)
		}
		err = ZipFolder("world.mcworld", outputName)
		if err != nil {
			logrus.Fatal(err)
		}
	}

	logrus.Infof("%d chunks", countChunks)
}

func writeGroupToJSON(rootGroup *mapGroup, filename string) error {
	// Create a mapJson instance to hold the root group data
	mapData := mapJson{
		Groups: make(map[string]groupJson),
	}

	// Convert the root group and its children to JSON data
	groupData := groupToJSON(rootGroup, ChunkPos{})

	// Add the root group JSON data to the mapJson instance
	mapData.Groups["root"] = groupData

	// Encode the mapJson instance as JSON and write it to a file
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(mapData); err != nil {
		return err
	}

	return nil
}

func groupToJSON(g *mapGroup, base ChunkPos) groupJson {
	offsetAbsolute := base.Add(g.offsetFromParent)
	// Create a groupJson instance to hold the group data
	groupData := groupJson{
		Name:           g.Name,
		Worlds:         make(map[string]worldJson),
		Groups:         make(map[string]groupJson),
		Size:           g.BoundsTotal(),
		OffsetAbsolute: offsetAbsolute,
	}

	// Convert the child worlds to JSON data and add them to the groupJson instance
	for name, world := range g.worlds {
		worldData := worldToJSON(world, offsetAbsolute)
		groupData.Worlds[name] = worldData
	}

	// Convert the child groups to JSON data and add them to the groupJson instance
	for name, childGroup := range g.groups {
		childData := groupToJSON(childGroup, offsetAbsolute)
		groupData.Groups[name] = childData
	}

	return groupData
}

func worldToJSON(w *worldMap, base ChunkPos) worldJson {
	// Create a worldJson instance to hold the world data
	worldData := worldJson{
		Name:           w.Name,
		Size:           w.BoundsTotal(),
		OffsetAbsolute: base.Add(w.offsetFromParent),
	}

	return worldData
}
