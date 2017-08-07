package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"sync"
	"time"

	"github.com/davecgh/go-spew/spew"
	flag "github.com/jessevdk/go-flags"
	"github.com/maplebed/libplum"
	"github.com/streadway/amqp"
)

const (
	stateFilePath = "/tmp/plum.state"
)

type Options struct {
	Daemon    bool `short:"d" long:"daemon" description:"run in daemon mode - writes config"`
	DumpState bool `short:"s" long:"state" description:"dump current known state"`
	Stream    bool `short:"S" long:"stream" description:"listen to the AMQP stream - broken"`
	Toggle    bool `short:"t" long:"toggle" description:"toggles all lights"`
	Rainbow   bool `short:"r" long:"rainbow" description:"sends the glow ring through the rainbow"`
	Update    bool `short:"u" description:"update state"`
}

func main() {
	var options Options
	flagParser := flag.NewParser(&options, flag.Default)
	flagParser.Parse()

	libplum.UserAgentAddition = "cli/0.1.0"

	stDump, err := ioutil.ReadFile(stateFilePath)
	if err != nil {
		panic(err)
	}
	st, err := libplum.LoadState(stDump)
	if err != nil {
		panic(err)
	}
	switch {
	case options.Daemon:
		daemon()
	case options.Stream:
		stream()
	case options.Toggle:
		toggleLights(st)
		writeState(st)
	case options.DumpState:
		spew.Dump(st)
	case options.Rainbow:
		rainbow(st)
	case options.Update:
		update(st)
	default:
		fmt.Println("need an action. run with --help for a list")
	}
}

func update(st libplum.State) {
	for _, house := range st.Houses {
		for _, room := range house.Rooms {
			for _, load := range room.LogicalLoads {
				for _, lightpad := range load.Lightpads {
					lightpad.Update(st.Conf)
				}
			}
		}
	}
}

func toggleLights(st libplum.State) {
	lps := libplum.Lightpads(st)
	wg := sync.WaitGroup{}
	for _, lp := range lps {
		if lp.Level > 50 {
			fmt.Printf("setting %s to %d\n", lp.LogicalLoad.Name, 0)
			wg.Add(1)
			go func() {
				err := lp.SetLoad(0)
				if err != nil {
					panic(err)
				}
				wg.Done()
			}()
		} else {
			fmt.Printf("setting %s to %d\n", lp.LogicalLoad.Name, 255)
			wg.Add(1)
			go func() {
				err := lp.SetLoad(255)
				if err != nil {
					panic(err)
				}
				wg.Done()
			}()
		}
	}
	wg.Wait()
}

func rainbow(st libplum.State) {
	lps := libplum.Lightpads(st)
	wg := sync.WaitGroup{}
	for _, lp := range lps {
		wg.Add(1)
		go func(pad *libplum.Lightpad) {
			glow := libplum.ForceGlow{
				LLID:      pad.LLID,
				Intensity: 1.0,
				Timeout:   3000,
			}
			mod := func(i float64) {
				glow.Red = int(math.Sin(0.1*i+0)*127 + 128)
				glow.Blue = int(math.Sin(0.2*i+1)*127 + 128)
				glow.Green = int(math.Sin(0.3*i+2)*127 + 128)
			}
			// go psychodelic
			for i := 0.0; i < 100; i++ {
				mod(i)
				spew.Dump(glow)
				go pad.SetLogicalLoadGlow(glow)
				time.Sleep(500 * time.Millisecond)
			}
			// and fade to black
			for i := 0.0; i < 10; i++ {
				glow.Intensity -= 0.1
				spew.Dump(glow)
				go pad.SetLogicalLoadGlow(glow)
				time.Sleep(800 * time.Millisecond)
			}
			wg.Done()
		}(lp)
	}
	wg.Wait()
}

func daemon() {
	rawConf, err := ioutil.ReadFile("./config.json")
	if err != nil {
		panic(err)
	}
	var c libplum.Config
	err = json.Unmarshal(rawConf, &c)
	if err != nil {
		panic(err)
	}
	st, _ := libplum.DiscoverState(c)
	spew.Dump(st)
	// writeState(st)
	stDump, err := ioutil.ReadFile(stateFilePath)
	if err != nil {
		panic(err)
	}
	st, err = libplum.LoadState(stDump)
	if err != nil {
		panic(err)
	}

	fmt.Println("starting listener")
	lps := libplum.Lightpads(st)
	ctx, cancel := context.WithCancel(context.Background())
	anncs := libplum.ListenForLightpads(ctx)
	for {
		select {
		case annc := <-anncs:
			fmt.Printf("\n%s got announcement: %+v\n", time.Now(), annc)
			if lp, ok := lps[annc.ID]; ok {
				lp.IP = annc.IP
				lp.Port = annc.Port
				writeState(st)
			} else {
				fmt.Printf("couldn't find lightpad %s\n", annc.ID)
			}
		default:
			fmt.Printf(".")
			for _, lp := range lps {
				lpm, err := lp.GetLogicalLoadMetrics()
				if err != nil {
					panic(err)
				}
				fmt.Printf("lpm %+v\n", lpm)
				for _, pad := range lpm.Metrics {
					if lp.ID == pad.ID {
						lp.Level = pad.Level
					}
				}
			}
			writeState(st)
			time.Sleep(4 * time.Second)
		}
	}
	fmt.Println("done waiting")
	cancel()
}

func stream() {
	conn, err := amqp.Dial("amqp://192.168.1.91:2708")
	if err != nil {
		panic(err)
	}
	fmt.Printf("conn %+v\n", conn)
}

func writeState(st libplum.State) {
	stDump, _ := libplum.DumpState(st)
	err := ioutil.WriteFile(stateFilePath, stDump, 0644)
	if err != nil {
		panic(err)
	}
}
