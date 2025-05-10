package discord

import (
	"context"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/maxhero/dhserverbot/dh"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type bot struct {
	ip             net.IP
	server         dh.Server
	userSelections map[string]string
	mu             sync.RWMutex
}

func ProcessBot(ctx context.Context, token string, ip net.IP, server dh.Server) {
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Println("Error creating Discord session:", err)
		return
	}

	b := &bot{
		ip:             ip,
		server:         server,
		userSelections: make(map[string]string),
	}

	// Register handlers for events
	dg.AddHandler(b.ready)
	dg.AddHandler(b.messageCreate)
	dg.AddHandler(b.interactionCreate)

	// Open a websocket connection to Discord
	err = dg.Open()
	if err != nil {
		log.Println("Error opening connection:", err)
		return
	}
	defer dg.Close()

	<-ctx.Done()
	log.Println("Closing connection...")
}

func (b *bot) ready(s *discordgo.Session, event *discordgo.Ready) {
	// Set the bot's status
	err := s.UpdateGameStatus(0, "Use start_dh to show options")
	if err != nil {
		log.Println("Error setting status:", err)
	}
	log.Printf("Logged in as: %v#%v\n", event.User.Username, event.User.Discriminator)
}

func (b *bot) messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore messages from the bot itself
	if m.Author.ID == s.State.User.ID {
		return
	}
	// Command to show the menu
	if m.Content == "start_dh" {
		b.showDropdownWithSubmitButton(s, m.ChannelID)
	}
}

func (b *bot) getDropDownOptions() []discordgo.SelectMenuOption {
	result := make([]discordgo.SelectMenuOption, 0, len(b.server.Maps()))
	for _, mapName := range b.server.Maps() {
		result = append(result, discordgo.SelectMenuOption{
			Label: "Start new " + mapName,
			Value: mapName,
		})
	}
	for _, session := range b.server.Sessions() {
		result = append(result, discordgo.SelectMenuOption{
			Label: "Stop " + session.MapName + " on port " + fmt.Sprint(session.Port) + " (started at " + session.Time.Format(time.TimeOnly) + ")",
			Value: "stop_" + fmt.Sprint(session.Port),
		})
	}
	return result
}

func (b *bot) showDropdownWithSubmitButton(s *discordgo.Session, channelID string) {
	_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: "Please select an option from the dropdown and then click Submit:",
		Components: []discordgo.MessageComponent{
			// Dropdown row
			&discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					&discordgo.SelectMenu{
						CustomID:    "option_select",
						Placeholder: "Choose an option",
						Options:     b.getDropDownOptions(),
					},
				},
			},
			// Submit button row (disabled by default)
			&discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					&discordgo.Button{
						CustomID: "submit_selection",
						Label:    "Submit",
						Style:    discordgo.PrimaryButton,
						Disabled: true,
					},
				},
			},
		},
	})

	if err != nil {
		log.Println("Error sending message:", err)
	}
}

func (b *bot) interactionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionMessageComponent:
		data := i.MessageComponentData()

		options := b.getDropDownOptions()
		if data.CustomID == "option_select" {
			// Handle dropdown selection
			userID := i.Member.User.ID
			selectedOption := data.Values[0]
			log.Println(i.Member.User.Username, "Selected option:", selectedOption)

			// Store the user's selection
			b.mu.Lock()
			b.userSelections[userID] = selectedOption
			b.mu.Unlock()

			for optionIdx := range options {
				options[optionIdx].Default = selectedOption == options[optionIdx].Value
			}
			// Update the message to enable submit button
			err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseUpdateMessage,
				Data: &discordgo.InteractionResponseData{
					Content: "Option selected! Now click the Submit button to confirm.",
					Components: []discordgo.MessageComponent{
						// Dropdown row (unchanged)
						&discordgo.ActionsRow{
							Components: []discordgo.MessageComponent{
								&discordgo.SelectMenu{
									CustomID:    "option_select",
									Placeholder: "Choose an option",
									Options:     options,
								},
							},
						},
						// Submit button row (now enabled)
						&discordgo.ActionsRow{
							Components: []discordgo.MessageComponent{
								&discordgo.Button{
									CustomID: "submit_selection",
									Label:    "Submit",
									Style:    discordgo.PrimaryButton,
									Disabled: false,
								},
							},
						},
					},
				},
			})

			if err != nil {
				log.Println("Error responding to dropdown selection:", err)
			}
		} else if data.CustomID == "submit_selection" {
			// Handle submit button
			userID := i.Member.User.ID

			b.mu.RLock()
			selectedOption, exists := b.userSelections[userID]
			b.mu.RUnlock()

			var response string
			if exists {
				response = "Please select a valid option first!"
				if strings.HasPrefix(selectedOption, "stop_") {
					port, err := strconv.Atoi(selectedOption[5:])
					if err != nil {
						response = "Error parsing port:" + err.Error()
					} else {
						log.Println(i.Member.User.Username, "Stoping server:", port)
						err = b.server.StopSession(uint16(port))
						if err != nil {
							response = "Error stopping server: " + err.Error()
						} else {
							response = "Server stopped on port " + fmt.Sprint(port) + "."
						}
					}
				} else {
					log.Println(i.Member.User.Username, "Starting server:", selectedOption)
					gameSession, err := b.server.NewSession(selectedOption)
					if err != nil {
						response = "Error starting server: " + err.Error()
					} else {
						response = "Server started on " + fmt.Sprint(b.ip) + ":" + fmt.Sprint(gameSession.Port) + " for " + gameSession.MapName + " map."
					}
				}
				// Clear the selection after submission
				b.mu.Lock()
				delete(b.userSelections, userID)
				b.mu.Unlock()
			} else {
				response = "You need to select an option first!"
			}

			err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: response,
				},
			})
			log.Println(response)

			if err != nil {
				log.Println("Error responding to submission:", err)
			}
		}
	}
}
