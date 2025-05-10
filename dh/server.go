package dh

import (
	"errors"
	"sync"
	"time"
)

var MaxSessionsCountError = errors.New("max sessions count reached")
var NoAvailablePortError = errors.New("no available port")
var NoSessionFoundError = errors.New("no session found")
var UnknownMapError = errors.New("unknown map")

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
	Sessions() []GameSession
	NewSession(mapName string) (GameSession, error)
	StopSession(port uint16) error
}

type PortRange struct {
	Start uint16
	End   uint16
}

type ServerConfig struct {
	Maps                  []Map
	Ports                 []PortRange
	ServerBinaryPath      string
	MaxConcurrentSessions uint
}

type server struct {
	maps                  []string
	mapNameToValue        map[string]string
	sessions              []GameSession
	maxConcurrentSessions uint
	ports                 map[uint16]bool
	serverBinary          string
	mutex                 sync.Mutex
}

func (s *server) Maps() []string {
	return s.maps
}

func (s *server) Sessions() []GameSession {
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
	for port, used := range s.ports {
		if !used {
			s.ports[port] = true
			session := GameSession{
				MapName: mapName,
				Time:    time.Now(),
				Port:    port,
			}
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
			s.sessions = append(s.sessions[:i], s.sessions[i+1:]...)
			s.ports[port] = false
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
		maxConcurrentSessions: config.MaxConcurrentSessions,
		ports:                 make(map[uint16]bool),
		serverBinary:          config.ServerBinaryPath,
	}
	for _, m := range config.Maps {
		result.maps = append(result.maps, m.Name)
		result.mapNameToValue[m.Name] = m.ServerValue
	}
	for _, portRange := range config.Ports {
		for i := portRange.Start; i <= portRange.End; i++ {
			result.ports[i] = false
		}
	}
	return result
}
