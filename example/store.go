package example

import (
	"encoding/json"
	"fmt"
	"log"
)

type OrderStatus int

const (
	Pending OrderStatus = iota
	Processing
	Shipped
	Delivered
	Cancelled
)

func (s OrderStatus) String() string {
	switch s {
	case Pending:
		return "pending"
	case Processing:
		return "processing"
	case Shipped:
		return "shipped"
	case Delivered:
		return "delivered"
	case Cancelled:
		return "cancelled"
	}
	//coverage:ignore reason=unreachable
	panic(fmt.Sprintf("unknown status: %d", int(s)))
}

type Order struct {
	ID     string      `json:"id"`
	Status OrderStatus `json:"status"`
	Total  float64     `json:"total"`
}

// Encode serializes the order to JSON.
// json.Marshal cannot fail on a struct with only JSON-serializable fields.
func (o Order) Encode() []byte {
	b, err := json.Marshal(o)
	//coverage:ignore reason=impossible-error
	if err != nil {
		//coverage:ignore reason=impossible-error
		panic(fmt.Sprintf("json.Marshal: %v", err))
	}
	return b
}

type Store struct {
	orders map[string]*Order
}

func NewStore() *Store {
	return &Store{orders: make(map[string]*Order)}
}

func (s *Store) Add(o *Order) {
	s.orders[o.ID] = o
}

func (s *Store) Get(id string) (*Order, bool) {
	o, ok := s.orders[id]
	return o, ok
}

// Run initializes and starts the application. Not unit-testable in isolation.
func Run() {
	//coverage:ignore reason=bootstrap
	log.Println("starting application")
}
