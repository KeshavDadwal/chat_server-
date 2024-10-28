package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/google/uuid"
)

type client struct {
	id     string
	name   string
	ch     chan string
	roomID string
}

type room struct {
	id       string             // Unique ID of the room
	name     string             // Name of the room
	clients  map[string]*client // Map of clients currently in the room, keyed by client ID
	messages []string           // Slice to store messages sent in the room
}

type server struct {
	clients       map[string]*client // maps clientID to client
	rooms         map[string]*room   // maps roomID to room
	clientsByName map[string]*client // maps clientName to client
	mu            sync.Mutex
}

func newServer() *server {
	return &server{
		clients:       make(map[string]*client),
		rooms:         make(map[string]*room),
		clientsByName: make(map[string]*client),
	}
}

func (s *server) start() {
	log.Println("Server started and ready to accept clients.")
}

// func (s *server) serveSSE(w http.ResponseWriter, r *http.Request) {
// 	flusher, ok := w.(http.Flusher)
// 	if !ok {
// 		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
// 		return
// 	}

// 	// Create a new client with a unique ID
// 	id := uuid.New().String()
// 	client := &client{
// 		id: id,
// 		ch: make(chan string),
// 	}

// 	// Add client to the server
// 	s.mu.Lock()
// 	s.clients[id] = client
// 	s.mu.Unlock()

// 	defer func() {
// 		s.mu.Lock()
// 		delete(s.clients, client.id)
// 		s.mu.Unlock()
// 	}()

// 	w.Header().Set("Content-Type", "text/event-stream")
// 	w.Header().Set("Cache-Control", "no-cache")
// 	w.Header().Set("Connection", "keep-alive")

// 	// Get the room ID from query parameters
// 	roomID := r.URL.Query().Get("roomID")
// 	client.roomID = roomID // Set the room ID in the client

// 	// Send user ID and room ID to the client (initial connection)
// 	fmt.Fprintf(w, "data33: {\"id\": \"%s\", \"roomID\": \"%s\"}\n\n", id, roomID)
// 	flusher.Flush()

// 	// Fetch and send existing messages for the room
// 	s.mu.Lock()
// 	room, exists := s.rooms[roomID]
// 	s.mu.Unlock()

// 	if exists {
// 		for _, message := range room.messages {
// 			fmt.Fprintf(w, "data22: %s\n\n", message)
// 			flusher.Flush()
// 		}
// 	}

// 	for {
// 		select {
// 		case message, ok := <-client.ch:
// 			if !ok {
// 				return
// 			}
// 			fmt.Fprintf(w, "data11: %s\n\n", message)
// 			flusher.Flush()
// 		case <-r.Context().Done():
// 			return
// 		}
// 	}
// }

func (s *server) serveSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Create a new client with a unique ID
	id := uuid.New().String()
	client := &client{
		id: id,
		ch: make(chan string),
	}

	// Add client to the server
	s.mu.Lock()
	s.clients[id] = client
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, client.id)
		s.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Get the room ID from query parameters
	roomID := r.URL.Query().Get("roomID")
	client.roomID = roomID // Set the room ID in the client

	// Fetch the room and its clients
	s.mu.Lock()
	room, exists := s.rooms[roomID]
	s.mu.Unlock()

	if !exists {
		http.Error(w, "Room not found", http.StatusNotFound)
		return
	}

	// Send existing messages for the room with usernames
	for _, message := range room.messages {
		fmt.Fprintf(w, "data: %s\n\n", message)
		flusher.Flush()
	}

	// Listen for new messages sent to this client's channel
	for {
		select {
		case message, ok := <-client.ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", message)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *server) sendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg struct {
		UserID  string `json:"userID"`
		RoomID  string `json:"roomID"`
		Content string `json:"message"`
	}

	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		log.Printf("Error decoding request body: %v", err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if msg.RoomID == "" || msg.UserID == "" {
		log.Println("Room ID and User ID are required")
		http.Error(w, "Room ID and User ID are required", http.StatusBadRequest)
		return
	}

	// Format the message
	message := fmt.Sprintf("%s: %s", msg.UserID, msg.Content)
	log.Printf("Sending message from user %s to room %s: %s", msg.UserID, msg.RoomID)

	s.mu.Lock()
	defer s.mu.Unlock()

	room, exists := s.rooms[msg.RoomID]
	if exists {
		// Store the message in the room
		room.messages = append(room.messages, message)

		// Send the message to the channel for all clients in this room
		for _, client := range room.clients {
			select {
			case client.ch <- message:
				log.Printf("Message sent to client %s in room %s", client.id, msg.RoomID)
			default:
				log.Printf("Client %s in room %s is not receiving messages (channel full)", client.id, msg.RoomID)
			}
		}
		log.Printf("Message stored in room %s", msg.RoomID)
	} else {
		log.Printf("Room %s not found", msg.RoomID)
		http.Error(w, "Room not found", http.StatusNotFound)
		return
	}

	// Respond with a success message
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "Message sent successfully"}); err != nil {
		log.Printf("Error encoding response: %v", err)
		return
	}

	log.Printf("Message sent successfully to room %s by user %s", msg.RoomID, msg.UserID)
}

func (s *server) createRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var requestData struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if requestData.Name == "" {
		http.Error(w, "Room name is required", http.StatusBadRequest)
		return
	}

	roomID := uuid.New().String()
	newRoom := &room{
		id:      roomID,
		name:    requestData.Name,
		clients: make(map[string]*client),
	}

	s.mu.Lock()
	s.rooms[roomID] = newRoom
	s.mu.Unlock()

	response := map[string]string{
		"message": fmt.Sprintf("Room '%s' created with ID %s", requestData.Name, roomID),
		"roomID":  roomID,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *server) listRooms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Create a slice to hold the room data.
	s.mu.Lock()
	var roomsData []map[string]interface{}
	for roomID, room := range s.rooms {
		// Create a list of clients in the current room.
		var clientsInRoom []map[string]string
		for _, client := range room.clients {
			clientsInRoom = append(clientsInRoom, map[string]string{
				"id":       client.id,
				"username": client.name,
			})
		}

		roomsData = append(roomsData, map[string]interface{}{
			"roomID":  roomID,
			"clients": clientsInRoom,
		})
	}
	s.mu.Unlock()

	// Respond with the room data in JSON format.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(roomsData)
}

func (s *server) joinRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var requestData struct {
		UserID string `json:"userID"`
		RoomID string `json:"roomID"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	user, userExists := s.clients[requestData.UserID]
	room, roomExists := s.rooms[requestData.RoomID]
	s.mu.Unlock()

	if !userExists {
		http.Error(w, "User not found", http.StatusBadRequest)
		return
	}
	if !roomExists {
		http.Error(w, "Room not found", http.StatusBadRequest)
		return
	}

	// Add the user to the room
	s.mu.Lock()
	room.clients[user.id] = user // Ensure user.id is a string
	s.mu.Unlock()

	response := map[string]string{
		"message": "User joined the room successfully",
		"roomID":  requestData.RoomID,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var requestData struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if requestData.Username == "" {
		http.Error(w, "Username is required", http.StatusBadRequest)
		return
	}

	userID := uuid.New().String()
	user := &client{
		id:   userID,
		name: requestData.Username,
		ch:   make(chan string),
	}

	s.mu.Lock()
	s.clients[userID] = user
	s.mu.Unlock()

	response := map[string]string{
		"message":  "User created successfully",
		"userID":   userID,
		"username": requestData.Username,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *server) listUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	var users []map[string]string
	for _, client := range s.clients {
		users = append(users, map[string]string{
			"id":       client.id,
			"username": client.name,
		})
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

func main() {
	srv := newServer()
	srv.start()

	http.HandleFunc("/events", srv.serveSSE)
	http.HandleFunc("/rooms", srv.listRooms)
	http.HandleFunc("/create-room", srv.createRoom)
	http.HandleFunc("/create-user", srv.createUser)
	http.HandleFunc("/join-room", srv.joinRoom)
	http.HandleFunc("/send", srv.sendMessage)
	http.HandleFunc("/list-users", srv.listUsers)

	log.Println("Starting server on :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal("ListenAndServe:", err)
	}
}
