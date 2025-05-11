package dh

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"
)

var MaxSessionsCountError = errors.New("max sessions count reached")
var NoAvailablePortError = errors.New("no available port")
var NoSessionFoundError = errors.New("no session found")
var UnknownMapError = errors.New("unknown map")
var SessionCreationError = errors.New("session creation error")

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
	MaxSessions   uint
}

type server struct {
	maps                  []string
	mapNameToValue        map[string]string
	sessions              []GameSession
	maxConcurrentSessions uint
	ports                 map[uint16]*exec.Cmd
	serverBinary          string
	defaultSessionParams  string
	initDoneSignature     string
	mutex                 sync.Mutex
}

func (s *server) Maps() []string {
	return s.maps
}

func (s *server) RunningSessions() []GameSession {
	return s.sessions
}

func (s *server) NewSession(mapName string) (GameSession, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.maxConcurrentSessions != 0 && len(s.sessions) >= int(s.maxConcurrentSessions) {
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
			go func(port uint16) {
				var wg sync.WaitGroup
				wg.Add(2)
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
						}
					}(pipe)
				}
				if err := cmd.Wait(); err != nil {
					log.Printf("DH server %v finished with error: %v\n", port, err)
				}
				wg.Wait()

				s.mutex.Lock()
				defer s.mutex.Unlock()
				for i := range s.sessions {
					if s.sessions[i].Port == port {
						s.sessions = append(s.sessions[:i], s.sessions[i+1:]...)
						break
					}
				}
				s.ports[port] = nil
				log.Printf("DH server %v done\n", port)
			}(port)
			s.ports[port] = cmd
			s.sessions = append(s.sessions, session)
			return session, nil
		}
	}
	return GameSession{}, NoAvailablePortError
}

func (s *server) StopSession(port uint16) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for i, session := range s.sessions {
		if session.Port == port {
			cmd := s.ports[port]
			if cmd != nil {
				err := cmd.Process.Kill()
				if err != nil {
					log.Printf("Error cancelling command: %v\n", err)
				}
			}
			s.ports[port] = nil
			s.sessions = append(s.sessions[:i], s.sessions[i+1:]...)
			return nil
		}
	}
	return NoSessionFoundError
}

func NewServer(config ServerConfig) Server {
	result := &server{
		maps:                  make([]string, 0, len(config.Maps)),
		mapNameToValue:        make(map[string]string, len(config.Maps)),
		sessions:              make([]GameSession, 0),
		maxConcurrentSessions: config.MaxSessions,
		ports:                 make(map[uint16]*exec.Cmd),
		serverBinary:          config.BinaryPath,
		defaultSessionParams:  config.SessionParams,
		initDoneSignature:     config.InitSignature,
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
