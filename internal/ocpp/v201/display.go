package v201

import "sync"

type DisplayMessage struct {
	ID       int
	Priority string
	State    string
	Text     string
	Language string
}

type DisplayMessageStore struct {
	mu       sync.RWMutex
	messages map[int]DisplayMessage
}

func NewDisplayMessageStore() *DisplayMessageStore {
	return &DisplayMessageStore{
		messages: make(map[int]DisplayMessage),
	}
}

func (s *DisplayMessageStore) Set(msg DisplayMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages[msg.ID] = msg
}

func (s *DisplayMessageStore) Get(id int) (DisplayMessage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msg, ok := s.messages[id]
	return msg, ok
}

func (s *DisplayMessageStore) Clear(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.messages[id]
	if ok {
		delete(s.messages, id)
	}
	return ok
}

func (s *DisplayMessageStore) GetAll() []DisplayMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]DisplayMessage, 0, len(s.messages))
	for _, m := range s.messages {
		result = append(result, m)
	}
	return result
}
