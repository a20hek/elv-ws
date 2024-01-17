package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	supa "github.com/nedpals/supabase-go"
	"github.com/spf13/viper"
)

type Message struct {
	MessageType string `json:"messageType"`
	Data        interface{} `json:"data"`
}

type Data struct {
  Name    string `json:"name"`
  Content string `json:"content"`
}

type CountData struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { 
		return true 
		},
}

var clients = make(map[*websocket.Conn]*sync.Mutex)

var supabase *supa.Client

func init() {

	viper.SetConfigFile("ENV")
	viper.ReadInConfig()
	viper.AutomaticEnv()

	supabase = supa.CreateClient(viper.GetString("SUPABASE_URL"), viper.GetString("SUPABASE_KEY"))


	ticker := time.NewTicker(1 * time.Second)

	go func() {
		for {
			select {
			case <-ticker.C:
				broadcastOnlineUsers()
			}
		}
	}()
}


func handleConnection(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade Error:", err)
		return
	}
	defer ws.Close()

	clients[ws] = &sync.Mutex{}

	for {
		_, p, err := ws.ReadMessage()
		if err != nil {
			log.Println(err)
			delete(clients, ws)
			return
		}

		var incomingMessage Message
		if err := json.Unmarshal(p, &incomingMessage); err != nil {
			log.Printf("error unmarshaling incoming message: %s\n", err)
			continue
		}

		broadcastMessage(Message{"chat", incomingMessage.Data})
		log.Printf("received: %s", p)
	}
}

func broadcastMessage(message Message) {
	var jsonData []byte
	
	jsonData, err := json.Marshal(message)
	if err != nil {
			log.Println("JSON marshal error:", err)
			return
	}

	for client,mutex := range clients {
			mutex.Lock()
			err := client.WriteMessage(websocket.TextMessage, jsonData)
			mutex.Unlock()
			if err != nil {
					log.Println(err)
					client.Close()
					delete(clients, client)
			}
	}
	if message.MessageType != "chat" {
		return
	}
	messageStr, ok := message.Data.(string)
	if !ok {
		log.Println("failed to cast message.Data to string")
		return
	}

	parts := strings.SplitN(messageStr, ": ", 2)
	if len(parts) < 2 {
		log.Println("received malformed chat message")
		return
	}

	name := strings.TrimPrefix(parts[0], "@")
	content := parts[1]

	row := Data{
		Name:    name,
		Content: content,
	}

	var results []Data
	dberr := supabase.DB.From("message").Insert(row).Execute(&results)

	if dberr != nil {
		log.Println("failed to insert into supabase: ", dberr)
		return
	}

	var countResults []CountData
	dberr = supabase.DB.From("count").Select("*").Eq("id", content).Execute(&countResults)

	if dberr != nil {
		log.Println("failed to fetch count from supabase: ", dberr)
		return
	}

	if len(countResults) > 0 {
		newCount := countResults[0].Count + 1
		dberr = supabase.DB.From("count").Update(map[string]interface{}{"count": newCount}).Eq("id", content).Execute(&countResults)
		
		if dberr != nil {
			log.Println("failed to update count in supabase: ", dberr)
			return
		}
	} else {
		dberr = supabase.DB.From("count").Insert(CountData{ID: content, Count: 1}).Execute(&countResults)
		
		if dberr != nil {
			log.Println("failed to insert new count in supabase: ", dberr)
			return
		}
	}
}

func broadcastOnlineUsers() {
	broadcastMessage(Message{"online", len(clients)})
}


func main() {
	port := fmt.Sprint(viper.Get("PORT"))

	http.HandleFunc("/ws", handleConnection)
	log.Println("server running at :8080")
	err := http.ListenAndServe(":"+port, nil)
	if err != nil { 
		log.Fatal("ListenAndServe: ", err)
	}
}