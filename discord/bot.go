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
	command        string
	ip             net.IP
	server         dh.Server
	userSelections map[string]string
	mu             sync.RWMutex
}

func ProcessBot(ctx context.Context, token string, command string, ip net.IP, server dh.Server) {
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Println("Error creating Discord session:", err)
		return
	}

	b := &bot{
		command:        command,
		ip:             ip,
		server:         server,
		userSelections: make(map[string]string),
	}

	dg.AddHandler(b.ready)
	dg.AddHandler(b.messageCreate)
	dg.AddHandler(b.interactionCreate)

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
	err := s.UpdateGameStatus(0, fmt.Sprintf("Use %v to show options", b.command))
	if err != nil {
		log.Println("Error setting status:", err)
	}
	log.Printf("Logged in as: %v#%v\n", event.User.Username, event.User.Discriminator)
}

func (b *bot) messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}
	if m.Content == b.command {
		b.showDropdownWithSubmitButton(s, m.ChannelID)
	}
}

func (b *bot) getDropDownOptions() []discordgo.SelectMenuOption {
	result := make([]discordgo.SelectMenuOption, 0, len(b.server.Maps()))
	for _, mapName := range b.server.Maps() {
		result = append(result, discordgo.SelectMenuOption{
			Label: fmt.Sprintf("Start new %v", mapName),
			Value: mapName,
		})
	}
	for _, session := range b.server.RunningSessions() {
		result = append(result, discordgo.SelectMenuOption{
			Label: fmt.Sprintf("Stop %v on port %v (started at %v)", session.MapName, session.Port, session.Time.Format(time.TimeOnly)),
			Value: fmt.Sprintf("stop_%v", session.Port),
		})
	}
	return result
}

func (b *bot) showDropdownWithSubmitButton(s *discordgo.Session, channelID string) {
	_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: "Select an option and then click the button",
		Components: []discordgo.MessageComponent{
			&discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					&discordgo.SelectMenu{
						CustomID:    "option_select",
						Placeholder: "Choose an option",
						Options:     b.getDropDownOptions(),
					},
				},
			},
			&discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					&discordgo.Button{
						CustomID: "submit_selection",
						Label:    "Go!",
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
		if data.CustomID == "option_select" {
			userID := i.Member.User.ID
			selectedOption := data.Values[0]

			b.mu.Lock()
			b.userSelections[userID] = selectedOption
			b.mu.Unlock()

			options := b.getDropDownOptions()
			for optionIdx := range options {
				options[optionIdx].Default = selectedOption == options[optionIdx].Value
			}
			err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseUpdateMessage,
				Data: &discordgo.InteractionResponseData{
					Components: []discordgo.MessageComponent{
						&discordgo.ActionsRow{
							Components: []discordgo.MessageComponent{
								&discordgo.SelectMenu{
									CustomID: "option_select",
									Options:  options,
								},
							},
						},
						&discordgo.ActionsRow{
							Components: []discordgo.MessageComponent{
								&discordgo.Button{
									CustomID: "submit_selection",
									Label:    "Go!",
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
			userID := i.Member.User.ID

			b.mu.RLock()
			selectedOption, exists := b.userSelections[userID]
			b.mu.RUnlock()

			response := "Wrong option selected!"
			if exists {
				if strings.HasPrefix(selectedOption, "stop_") {
					port, err := strconv.Atoi(selectedOption[5:])
					if err != nil {
						response = fmt.Sprintf("Error parsing port: %v", err)
					} else {
						log.Println(i.Member.User, "Stoping server:", port)
						err = b.server.StopSession(uint16(port))
						if err != nil {
							response = fmt.Sprintf("Error stopping server: %v", err)
						} else {
							response = fmt.Sprintf("Server stopped on port %v", port)
						}
					}
				} else {
					log.Println(i.Member.User, "Starting server:", selectedOption)
					response = "Starting server..."
					go func() {
						gameSession, err := b.server.NewSession(selectedOption)
						if err != nil {
							response = fmt.Sprintf("Error starting server: %v", err)
						} else {
							response = fmt.Sprintf("Server started on %v:%v for %v map", b.ip, gameSession.Port, gameSession.MapName)
						}
						response = i.Member.User.Mention() + " " + response
						_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
							Content: &response,
						})
						if err != nil {
							log.Printf("Error editing interaction response: %v", err)
							return
						}
					}()
				}
				b.mu.Lock()
				delete(b.userSelections, userID)
				b.mu.Unlock()
			}

			err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseUpdateMessage,
				Data: &discordgo.InteractionResponseData{
					Content: i.Member.User.Mention() + " " + response,
				},
			})
			log.Println(response)

			if err != nil {
				log.Println("Error responding to submission:", err)
			}
		}
	}
}
