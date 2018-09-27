package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/nlopes/slack"
)

var custom CustomResponses = nil
var config Config

type Config struct {
	MtgApiEndpoint     string `json:"mtg_api_endpoint"`
	CustomResponseFile string `json:"custom_path"`
	SlackApiKey        string `json:"slack_key"`
}

type CustomResponses []struct {
	Trigger  string   `json:"trigger"`
	Response []string `json:"response"`
}

type Card struct {
	Name         string `json:"name"`
	MultiverseId int    `json:"multiverseid"`
	Set          string `json"set"`
	SetName      string `json:"setName"`
	ImageUrl     string `json:"imageUrl,omitempty"`
	Rarity       string `json:"rarity"`
}

type Cards struct {
	Card []Card `json:"cards"`
}

// returns if a given card rarity is allowed to be returned
func allowedCardRarity(rarity string) bool {
	// make a map of allowed card rarities from the mtg api
	// this will filter out things like promo cards and masterpieces
	m := make(map[string]bool)
	m["Common"] = true
	m["Uncommon"] = true
	m["Rare"] = true
	m["Mythic Rare"] = true
	m["Basic Land"] = true

	return m[rarity]
}

// loads custom trigger/response pairs from the custom file specified in the config
func loadCustomResponses() {
	raw, err := ioutil.ReadFile(config.CustomResponseFile)
	if err != nil {
		log.Println("loadCustomResponses: ", err)
		os.Exit(1)
	}

	json.Unmarshal(raw, &custom)
}

// loads the config file into our config struct
func loadConfig(configPath string) {
	raw, err := ioutil.ReadFile(configPath)
	if err != nil {
		log.Println("loadConfig: ", err)
		os.Exit(1)
	}

	json.Unmarshal(raw, &config)
}

// leverage the api defined in the config to fetch links to the gatherer image of a card
// NOTE: currently only works with api.magicthegathering.io/v1/
func fetchCard(cardName string) string {
	card_name := url.QueryEscape(cardName)
	uri := fmt.Sprintf(config.MtgApiEndpoint, card_name)

	mtgClient := &http.Client{}

	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		log.Println("NewRequest: ", err)
		return ""
	}

	resp, err := mtgClient.Do(req)
	if err != nil {
		log.Println("Do: ", err)
		return ""
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println(err)
	}

	var cc Cards

	defer resp.Body.Close()

	if err := json.Unmarshal(data, &cc); err != nil {
		fmt.Println("Decode: ", err)
		fmt.Println(data)
		return ""
	}

	var cardToReturn Card
	regString := fmt.Sprintf("(?i)^%s", cardName)
	reg := regexp.MustCompile(regString)

	/*
		check for matches as follows:
		exact name match (case insensitive):
		 	return immediately
		regex for `(?i)^cardname`, and current card set to return doesnt match:
		 	set current card to be returned
		no card is currently set to be returned:
		 	set the current card to be returned
	*/
	for i := range cc.Card {
		// we need to go through in reverse because the api returns cards sorted ascending
		// and we want the most recent printing
		c := cc.Card[len(cc.Card)-1-i]
		if c.ImageUrl != "" && allowedCardRarity(c.Rarity) {
			if strings.EqualFold(c.Name, cardName) {
				return c.ImageUrl
			} else if reg.MatchString(c.Name) && !reg.MatchString(cardToReturn.Name) {
				cardToReturn = c
			} else if cardToReturn.Name == "" {
				cardToReturn = c
			}
		}
	}

	return cardToReturn.ImageUrl
}

// returns an arary of strings that were encapsulated by [[string_here]]
func getStringsFromMessage(message string) []string {
	reg := regexp.MustCompile(`\[\[[\w ,.!?:\-\(\)\/'"]+\]\]`)
	matches := reg.FindAllStringSubmatch(message, -1)

	if len(matches) == 0 {
		return nil
	}

	ret := make([]string, len(matches))

	for index, match := range matches {
		trimmed_string := strings.Trim(match[0], "[]")
		ret[index] = trimmed_string
	}

	return ret
}

// checks the custom response json, and returns a random response for the given trigger
func checkCustomResponseMatches(message string) string {
	ret := ""
	if custom != nil {
		for _, c := range custom {
			reg := regexp.MustCompile(c.Trigger)
			if reg.MatchString(message) {
				// if there is more than one response for a given trigger then print one at random
				rand.Seed(time.Now().UTC().UnixNano())
				ret = ret + c.Response[rand.Intn(len(c.Response))] + "\n"
			}
		}
	}
	return ret
}

// takes a slack message and determines if we need to respond
// NOTE: custom responses override card fetches
func processMessage(message string) string {
	ret := checkCustomResponseMatches(message)
	if ret != "" {
		return ret
	}

	items := getStringsFromMessage(message)
	if items != nil {
		for _, s := range items {
			ret = ret + fetchCard(s) + "\n"
		}
	}

	return ret
}

// watches slack for events and acts on them
func slackStuff() {
	api := slack.New(config.SlackApiKey)
	logger := log.New(os.Stdout, "slack-bot: ", log.Lshortfile|log.LstdFlags)
	slack.SetLogger(logger)
	// api.SetDebug(true)

	rtm := api.NewRTM()
	go rtm.ManageConnection()

	for msg := range rtm.IncomingEvents {
		switch ev := msg.Data.(type) {
		case *slack.MessageEvent:
			response := processMessage(ev.Text)
			if strings.Trim(response, "\n") != "" {
				params := slack.PostMessageParameters{
					AsUser:      true,
					UnfurlLinks: true,
					UnfurlMedia: true,
				}
				api.PostMessage(ev.Channel, response, params)
			}
			break
		default:
			// do nothing
		}
	}
}

func main() {
	if len(os.Args) == 2 {
		fmt.Printf("Loading config from '%s'\n", os.Args[1])
		loadConfig(os.Args[1])
	} else {
		fmt.Println("Loading config from './config.json'")
		loadConfig("/home/ezimmer/go/src/mtgbot-golang/config.json")
	}

	if config.CustomResponseFile != "" {
		fmt.Printf("Loading custom responses from '%s'\n", config.CustomResponseFile)
		loadCustomResponses()
	}
	slackStuff()
}
