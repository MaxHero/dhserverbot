package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/maxhero/dhserverbot/dh"
	"github.com/maxhero/dhserverbot/discord"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	ip := flag.String("ip", "", "IP address of server. Needed for Discord integration")
	port := flag.String("port", "7777", "Ports (comma separated, single or range (e.g., 7777,8000-9000,11111)")
	maxSessions := flag.Uint("max_sessions", 0, "Maximum number of concurrent sessions. 0 means no limit")
	binaryPath := flag.String("binary_path", "", "Path to WindowsServer\\DreadHunger\\Binaries\\Win64\\DreadHungerServer-Win64-Shipping.exe")
	sessionParams := flag.String("session_params", "maxplayers=8", "Default session parameters")
	initSignature := flag.String("init_signature", "LogLoad: (Engine Initialization) Total Time:", "DH server log signature meaning initialization is done")
	initTimeout := flag.Duration("init_timeout", 30*time.Second, "DH server initialization timeout")
	maps := flag.String("maps", "", "Comma separated list of maps. Approach=Approach_Persistent,Departure=Departure_Persistent,Expanse=Expanse_Persistent")
	discordToken := flag.String("discord_token", "", "Discord bot token")
	discordCommand := flag.String("discord_command", "!start_dh", "Discord bot command to show the menu")

	flag.Parse()

	parsedIP := net.ParseIP(*ip)
	if parsedIP == nil {
		fmt.Println("Error: IP address is required.")
		flag.Usage()
		return
	}

	var config dh.ServerConfig

	for _, portRange := range strings.Split(*port, ",") {
		portSplit := strings.Split(portRange, "-")
		if len(portSplit) == 1 {
			p, err := strconv.Atoi(portSplit[0])
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				flag.Usage()
				return
			}
			config.Ports = append(config.Ports, dh.PortRange{Start: uint16(p), End: uint16(p)})
		} else if len(portSplit) == 2 {
			s, err := strconv.Atoi(portSplit[0])
			if err != nil || s < 0 || s > 65535 {
				fmt.Printf("Wrong port range: %v\n", portRange)
				flag.Usage()
				return
			}
			e, err := strconv.Atoi(portSplit[1])
			if err != nil || e < 0 || e > 65535 {
				fmt.Printf("Wrong port range: %v\n", portRange)
				flag.Usage()
				return
			}
			if s > e {
				fmt.Printf("Wrong port range: %v\n", portRange)
				flag.Usage()
				return
			}
			config.Ports = append(config.Ports, dh.PortRange{Start: uint16(s), End: uint16(e)})
		}
	}
	config.MaxSessions = *maxSessions

	if *binaryPath == "" {
		fmt.Println("Error: Dread Hunger binary path is required.")
		flag.Usage()
		return
	}
	config.BinaryPath = *binaryPath

	if *initSignature == "" {
		fmt.Println("Error: Dread Hunger init done signature is required.")
		flag.Usage()
		return
	}
	config.InitSignature = *initSignature
	config.InitTimeout = *initTimeout

	if len(*maps) == 0 {
		fmt.Println("Error: at least one map is required.")
		flag.Usage()
	}
	for _, mapName := range strings.Split(*maps, ",") {
		mapSplit := strings.Split(mapName, "=")
		if len(mapSplit) != 2 {
			fmt.Printf("Wrong map: %v\n", mapName)
			flag.Usage()
		}
		config.Maps = append(config.Maps, dh.Map{Name: mapSplit[0], ServerValue: mapSplit[1]})
	}
	if len(config.Maps) == 0 {
		fmt.Println("Error: at least one map is required.")
		flag.Usage()
		return
	}
	if *discordToken == "" {
		fmt.Println("Error: Discord bot token is required.")
		flag.Usage()
		return
	}
	if *discordCommand == "" {
		fmt.Println("Error: Discord bot command is required.")
		flag.Usage()
		return
	}
	config.SessionParams = *sessionParams
	server := dh.NewServer(config)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go discord.ProcessBot(ctx, *discordToken, *discordCommand, parsedIP, server)

	fmt.Println("Application is now running. Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
}
