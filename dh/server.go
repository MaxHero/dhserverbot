package dh

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var MaxSessionsCountError = errors.New("max sessions count reached")
var NoAvailablePortError = errors.New("no available port")
var NoSessionFoundError = errors.New("no session found")
var UnknownMapError = errors.New("unknown map")
var SessionCreationError = errors.New("session creation error")
var TimeoutError = errors.New("timeout")

type Map struct {
	Name        string
	ServerValue string
}

type GameSession struct {
	MapName string
	Time    time.Time
	Port    uint16
}

type Server interface {
	Maps() []string
	RunningSessions() []GameSession
	NewSession(mapName string) (GameSession, error)
	StopSession(port uint16) error
}

type PortRange struct {
	Start uint16
	End   uint16
}

type ServerConfig struct {
	Maps          []Map
	Ports         []PortRange
	BinaryPath    string
	SessionParams string
	InitSignature string
	InitTimeout   time.Duration
	MaxSessions   uint
}

type server struct {
	maps                  []string
	mapNameToValue        map[string]string
	runningSessions       []GameSession
	maxConcurrentSessions uint
	ports                 map[uint16]*exec.Cmd
	serverBinary          string
	defaultSessionParams  string
	initSignature         string
	initTimeout           time.Duration
	mutex                 sync.Mutex
}

func (s *server) Maps() []string {
	return s.maps
}

func (s *server) RunningSessions() []GameSession {
	return s.runningSessions
}

func (s *server) NewSession(mapName string) (GameSession, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.maxConcurrentSessions != 0 && len(s.runningSessions) >= int(s.maxConcurrentSessions) {
		return GameSession{}, MaxSessionsCountError
	}
	_, exists := s.mapNameToValue[mapName]
	if !exists {
		return GameSession{}, UnknownMapError
	}
	for port, cmd := range s.ports {
		if cmd == nil {
			session := GameSession{
				MapName: mapName,
				Time:    time.Now(),
				Port:    port,
			}
			args := fmt.Sprintf("%v?%v?port=%v -log", s.mapNameToValue[mapName], s.defaultSessionParams, port)
			log.Printf("Starting DH server %v with args: %v\n", port, args)
			cmd := exec.Command(s.serverBinary, args)
			stdoutPipe, err := cmd.StdoutPipe()
			if err != nil {
				log.Printf("Error creating stdout pipe: %v\n", err)
				return GameSession{}, SessionCreationError
			}

			stderrPipe, err := cmd.StderrPipe()
			if err != nil {
				log.Printf("Error creating stderr pipe: %v\n", err)
				return GameSession{}, SessionCreationError
			}

			if err := cmd.Start(); err != nil {
				log.Printf("Error starting command: %v\n", err)
				return GameSession{}, SessionCreationError
			}

			log.Printf("Child process started")
			initDone := make(chan struct{})
			go func(port uint16) {
				var wg sync.WaitGroup
				wg.Add(2)
				once := sync.Once{}
				for _, pipe := range [...]io.ReadCloser{stdoutPipe, stderrPipe} {
					go func(pipe io.ReadCloser) {
						defer wg.Done()
						reader := bufio.NewReader(pipe)
						for {
							line, err := reader.ReadString('\n')
							if err != nil {
								if err == io.EOF {
									break
								}
								log.Printf("Error reading stdout: %v\n", err)
								break
							}
							log.Printf("DH server %v output: %v", port, line)
							if strings.Contains(line, s.initSignature) {
								once.Do(func() {
									log.Printf("DH server %v init done\n", port)
									close(initDone)
								})
							}
						}
					}(pipe)
				}

				if err := cmd.Wait(); err != nil {
					log.Printf("DH server %v finished with error: %v\n", port, err)
				}
				wg.Wait()

				s.mutex.Lock()
				defer s.mutex.Unlock()
				for i := range s.runningSessions {
					if s.runningSessions[i].Port == port {
						s.runningSessions = append(s.runningSessions[:i], s.runningSessions[i+1:]...)
						break
					}
				}
				s.ports[port] = nil
				log.Printf("DH server %v done\n", port)
			}(port)
			select {
			case <-time.After(s.initTimeout):
				log.Printf("DH server %v init timeout\n", port)
				cmd.Process.Kill()
				return GameSession{}, TimeoutError
			case <-initDone:
				s.ports[port] = cmd
				s.runningSessions = append(s.runningSessions, session)
				return session, nil
			}
		}
	}
	return GameSession{}, NoAvailablePortError
}

func (s *server) StopSession(port uint16) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for i, session := range s.runningSessions {
		if session.Port == port {
			cmd := s.ports[port]
			if cmd != nil {
				err := cmd.Process.Kill()
				if err != nil {
					log.Printf("Error cancelling command: %v\n", err)
				}
			}
			s.ports[port] = nil
			s.runningSessions = append(s.runningSessions[:i], s.runningSessions[i+1:]...)
			return nil
		}
	}
	return NoSessionFoundError
}

func NewServer(config ServerConfig) Server {
	result := &server{
		maps:                  make([]string, 0, len(config.Maps)),
		mapNameToValue:        make(map[string]string, len(config.Maps)),
		runningSessions:       make([]GameSession, 0),
		maxConcurrentSessions: config.MaxSessions,
		ports:                 make(map[uint16]*exec.Cmd),
		serverBinary:          config.BinaryPath,
		defaultSessionParams:  config.SessionParams,
		initSignature:         config.InitSignature,
		initTimeout:           config.InitTimeout,
	}
	for _, m := range config.Maps {
		result.maps = append(result.maps, m.Name)
		result.mapNameToValue[m.Name] = m.ServerValue
	}
	for _, portRange := range config.Ports {
		for i := portRange.Start; i <= portRange.End; i++ {
			result.ports[i] = nil
		}
	}
	return result
}
