package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/df-mc/dragonfly/server/world"
	"github.com/df-mc/dragonfly/server/world/mcdb"
	"github.com/df-mc/goleveldb/leveldb/opt"
	"github.com/sirupsen/logrus"
)

var pre117 = true
var outputName = "world-out"

type mapGroup struct {
	Name   string
	worlds map[string]*worldMap
	groups map[string]*mapGroup

	boundsTotal      ChunkPos
	offsetFromParent ChunkPos
}

type worldMap struct {
	Name string
	*mcdb.Provider
	boundsMin        ChunkPos
	boundsMax        ChunkPos
	boundsTotal      ChunkPos
	offsetFromParent ChunkPos
}

type worldJson struct {
	Name   string
	Size   ChunkPos
	Offset ChunkPos
}

type groupJson struct {
	Name   string
	Worlds map[string]worldJson
	Groups map[string]groupJson

	Size   ChunkPos
	Offset ChunkPos
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

	w.boundsTotal = ChunkPos{
		w.boundsMax[0] - w.boundsMin[0] + 1,
		w.boundsMax[1] - w.boundsMin[1] + 1,
	}
}

func addWorlds(prov *mcdb.Provider, baseOffset ChunkPos, worlds map[string]*worldMap) error {
	for _, w := range worlds {
		var outputOffset = baseOffset.Add(w.offsetFromParent)

		for pos := range w.Provider.Chunks(pre117) {
			c, exists, err := w.Provider.LoadChunk(pos.P, pos.D)
			if err != nil {
				return err
			}
			if !exists {
				panic("doesnt exist!!!")
			}

			blockNBT, err := w.Provider.LoadBlockNBT(pos.P, pos.D)
			if err != nil {
				return err
			}

			for _, v := range blockNBT {
				v["x"] = v["x"].(int32) + outputOffset.X()*16
				v["z"] = v["z"].(int32) + outputOffset.Z()*16
			}

			entities, err := w.Provider.LoadEntities(pos.P, pos.D, &EntityRegistry{})
			if err != nil {
				return err
			}

			for _, e := range entities {
				ent := e.(*DummyEntity)
				entt := ent.T.(*DummyEntityType)
				entt.NBT["Pos"].([]any)[0] = entt.NBT["Pos"].([]any)[0].(float32) + float32(outputOffset.X()*16)
				entt.NBT["Pos"].([]any)[2] = entt.NBT["Pos"].([]any)[2].(float32) + float32(outputOffset.Z()*16)
			}

			var posOut = (world.ChunkPos)(outputOffset.Add(pos.P))

			err = prov.SaveChunk(posOut, c, world.Overworld)
			if err != nil {
				return err
			}

			err = prov.SaveBlockNBT(posOut, blockNBT, world.Overworld)
			if err != nil {
				return err
			}

			err = prov.SaveEntities(posOut, entities, world.Overworld)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func addGroups(prov *mcdb.Provider, baseOffset ChunkPos, groups map[string]*mapGroup) error {
	for _, group := range groups {
		if len(group.groups) > 0 {
			err := addGroups(prov, baseOffset.Add(group.offsetFromParent), group.groups)
			if err != nil {
				return err
			}
		}
		err := addWorlds(prov, baseOffset.Add(group.offsetFromParent), group.worlds)
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
			os.MkdirAll(s, 0755)
			err := UnpackZip(filepath, s)
			if err != nil {
				return err
			}
			filepath = s
		} else {
			return fmt.Errorf("%s is not mcworld", filepath)
		}

		prov, err := mcdb.New(logrus.StandardLogger(), filepath, opt.DefaultCompression)
		if err != nil {
			return err
		}
		w := &worldMap{Name: worldName, Provider: prov}
		w.calcBounds()
		group.worlds[worldName] = w
		return nil
	}
	return recursiveAddWorld(filepath, parts, group.groups)
}

func layoutWorld(w *worldMap, offset ChunkPos) {
	w.offsetFromParent = offset
}

func layoutGroup(g *mapGroup, parentOffset ChunkPos, padding int) {
	// First, layout the children
	currentOffset := parentOffset
	currentRowHeight := 0
	currentColWidth := 0
	for _, childGroup := range g.groups {
		layoutGroup(childGroup, currentOffset, padding)
		childWidth := int(childGroup.boundsTotal[0])
		if childWidth > currentColWidth {
			currentColWidth = childWidth
		}
		childHeight := int(childGroup.boundsTotal[1])
		if childHeight > currentRowHeight {
			currentRowHeight = childHeight
		}
		childGroup.offsetFromParent = currentOffset
		currentOffset[0] += int32(childWidth)
	}
	for _, childWorld := range g.worlds {
		layoutWorld(childWorld, currentOffset)
		childWidth := int(childWorld.boundsTotal[0])
		if childWidth > currentColWidth {
			currentColWidth = childWidth
		}
		childHeight := int(childWorld.boundsTotal[1])
		if childHeight > currentRowHeight {
			currentRowHeight = childHeight
		}
		childWorld.offsetFromParent = currentOffset
		currentOffset[0] += int32(childWidth + padding)
	}

	// Then, calculate the size and position of this group based on its children
	g.boundsTotal = currentOffset.Sub(parentOffset)
}

func main() {
	os.RemoveAll("tmp")
	os.RemoveAll(outputName)

	worldPaths, err := filepath.Glob("inputs/**/*")
	if err != nil {
		logrus.Fatal(err)
	}

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
	root := &mapGroup{groups: worldGroups}
	layoutGroup(root, ChunkPos{}, 80)

	{ // output world
		providerOut, err := mcdb.New(logrus.StandardLogger(), outputName, opt.DefaultCompression)
		if err != nil {
			logrus.Fatal(err)
		}
		err = addGroups(providerOut, ChunkPos{}, worldGroups)
		if err != nil {
			logrus.Fatal(err)
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
	}

	err = writeGroupToJSON(root, "map.json")
	if err != nil {
		logrus.Fatal(err)
	}
}

func writeGroupToJSON(rootGroup *mapGroup, filename string) error {
	// Create a mapJson instance to hold the root group data
	mapData := mapJson{
		Groups: make(map[string]groupJson),
	}

	// Convert the root group and its children to JSON data
	groupData := groupToJSON(rootGroup)

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

func groupToJSON(g *mapGroup) groupJson {
	// Create a groupJson instance to hold the group data
	groupData := groupJson{
		Name:   g.Name,
		Worlds: make(map[string]worldJson),
		Groups: make(map[string]groupJson),
		Size:   g.boundsTotal,
		Offset: g.offsetFromParent,
	}

	// Convert the child worlds to JSON data and add them to the groupJson instance
	for name, world := range g.worlds {
		worldData := worldToJSON(world)
		groupData.Worlds[name] = worldData
	}

	// Convert the child groups to JSON data and add them to the groupJson instance
	for name, childGroup := range g.groups {
		childData := groupToJSON(childGroup)
		groupData.Groups[name] = childData
	}

	return groupData
}

func worldToJSON(w *worldMap) worldJson {
	// Create a worldJson instance to hold the world data
	worldData := worldJson{
		Name:   w.Name,
		Size:   w.boundsTotal,
		Offset: w.offsetFromParent,
	}

	return worldData
}
