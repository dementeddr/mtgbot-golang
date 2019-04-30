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
	"database/sql"
	"github.com/nlopes/slack"
	_ "github.com/mattn/go-sqlite3"
)

var custom CustomResponses = nil
var config Config

type Config struct {
	MtgApiNameOnly      string `json:"mtg_api_name_only"`
	MtgApiNameAndSet    string `json:"mtg_api_name_set"`
	MtgApiNameAndCode   string `json:"mtg_api_name_code"`
	CustomResponseFile  string `json:"custom_path"`
	SlackApiKey         string `json:"slack_key"`
}

type CustomResponses []struct {
	Trigger  string   `json:"trigger"`
	Response []string `json:"response"`
}

type Card struct {
	Name         string `json:"name"`
	MultiverseId int    `json:"multiverseid"`
	Set          string `json:"set"`
	SetName      string `json:"setName"`
	ImageUrl     string `json:"imageUrl,omitempty"`
	Rarity       string `json:"rarity"`
	Type         string `json:"type,omitempty"`
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
	m["Mythic"] = true
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

// Does the actuall GET request for magic cards and cleanup afterward. Returns a Cards struct
// if successful. Returns nil if there are any errors
func callMtgAPI(uri string) *Cards {

	mtgClient := &http.Client{}

	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		log.Println("NewRequest: ", err)
		return nil
	}

	resp, err := mtgClient.Do(req)
	if err != nil {
		log.Println("Do: ", err)
		return nil
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println(err)
		return nil
	}

	var cc Cards

	defer resp.Body.Close()

	if err := json.Unmarshal(data, &cc); err != nil {
		fmt.Println("Decode: ", err)
		fmt.Println(data)
		return nil
	}

	return &cc
}

// leverage the api defined in the config to fetch links to the gatherer image of a card
// NOTE: currently only works with api.magicthegathering.io/v1/
func fetchCard(searchString string) string {

	search_split := strings.Split(searchString,"|")
	card_name := search_split[0]

	url_card_name := url.QueryEscape(card_name)
	//url_card_name := url.QueryEscape(search_split[0])

	uri := ""
	//set_name := ""

	// If there's a single divider, determine if it's a set name or set code
	if len(search_split) == 2 {
		set_code_reg := regexp.MustCompile(`^[0-9A-Za-z]{2,3}$`)
		set_code := set_code_reg.FindString(search_split[1])
		if len(set_code) > 0 {
			uri = fmt.Sprintf(config.MtgApiNameAndCode, url_card_name, set_code)
		} else if len(set_code) == 0 {
			uri = fmt.Sprintf(config.MtgApiNameAndSet, url_card_name, url.QueryEscape(search_split[1]))
		} else {
			fmt.Println("Set Code regex returned negative length string: %d\n", len(set_code))
			return "Shit... I think fucked up bad, guys."
		}
	// If there's zero or more than one dividers, just search with the name
	} else {
		uri = fmt.Sprintf(config.MtgApiNameOnly, url_card_name)
	}

	cc := callMtgAPI(uri)
	if cc == nil {
		return "Error fetching card from API. See command line output."
	}

	if len (cc.Card) == 0 {
		return "Card not found :("
	}

	var cardToReturn Card
	regString := fmt.Sprintf("(?i)^%s(,| )", card_name)
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
		if c.ImageUrl != "" && allowedCardRarity(c.Rarity) && c.Type != "Vanguard" {
			if strings.EqualFold(c.Name, card_name) {
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

// returns an array of strings that were encapsulated by [[string_here]]
func getMTGStringsFromMessage(message string) []string {
	mtg_reg := regexp.MustCompile(`\[\[[\w ,.!?:\-\|\(\)\/'"]+\]\]`)
	matches := mtg_reg.FindAllStringSubmatch(message, -1)

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


// returns an array of strings that were encapsulated by <string_here>
func getDNDStringsFromMessage(message string) []string {

	dnd_reg := regexp.MustCompile(`\&lt;[\w ']+\&gt;`)
	matches := dnd_reg.FindAllStringSubmatch(message, -1)

	if len(matches) == 0 {
		return nil
	}

	ret := make([]string, len(matches))

	for index, match := range matches {
		trimmed_string := strings.TrimPrefix(match[0], "&lt;") // &lt; = '<'
		trimmed_string = strings.TrimSuffix(trimmed_string, "&gt;") // &gt; = '>'
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
				return ret + c.Response[rand.Intn(len(c.Response))] + "\n"

			}
		}
	}
	return ret
}


// Takes the name of a DnD spell, queries the database for it's into, and formats that text
// so it can be posted by mtgbot
func formatDnDSpellText(name string, database *sql.DB) string {

	var cased_name = ""
	var level = ""
	var school = ""
	var desc = ""
	var build strings.Builder

	row, err := database.Query("SELECT Name, Level, School FROM Spells_Main WHERE Name=?", name)
	if (err != nil) {
		fmt.Println(err)
		return ""
	}

	row.Next()
	row.Scan(&cased_name, &level, &school)

	fmt.Fprintf(&build, ">*%s*\n>_Level %s %s Spell_\n", cased_name, level, school)

	row, err = database.Query("SELECT Description FROM Spells_Desc WHERE Name=?", name)
	if (err != nil) {
		fmt.Println(err)
		return ""
	}

	row.Next()
	row.Scan(&desc)

	fmt.Fprintf(&build, ">%s", desc)

	build.WriteString("")
	return build.String()
}

// takes a slack message and determines if we need to respond
// NOTE: custom responses override card fetches
func processMessage(message string) string {
	ret := checkCustomResponseMatches(message)
	if ret != "" {
		return ret
	}

	mtg_cards := getMTGStringsFromMessage(message)
	if mtg_cards != nil {
		for _, s := range mtg_cards {
			ret = ret + fetchCard(s) + "\n"
		}
	}

	dnd_spells := getDNDStringsFromMessage(message)

	if dnd_spells != nil {
		for _, s := range dnd_spells{

			database, err := sql.Open("sqlite3", "./dndbot.db")
			if (err != nil) {
				fmt.Println(err)
				return ""
			}
			rows, err := database.Query("SELECT Name FROM Spells_Desc WHERE Name LIKE '%' || ? || '%'", s)
			if (err != nil) {
				fmt.Println(err)
				return ""
			}
			if (rows == nil) {
				return ""
			}

			// Find the entry with the exact name. Barring that, take the last entry that the Compare function says is "greater" than the searched name. For whatever that's worth.
			var row_name = "" // Name value in the row from SQL
			var closeness = -2 // Return value from the closest comparison so far
			var close_name = "" // Closest name so far, for whatever that's worth

			for rows.Next() {
				rows.Scan(&row_name)
				var comp_val = strings.Compare(strings.ToLower(row_name), strings.ToLower(s))

				// This means we found an exact match
				if (comp_val == 0) {
					close_name = row_name
					break
				}

				if (comp_val > closeness) {
					close_name = row_name
					closeness = comp_val
				}
			}

			if (close_name != "") {
				ret = formatDnDSpellText(close_name, database)
			}

			return ret
		}
	}

	return ret
}

// watches slack for events and acts on them
func slackStuff() {
	logger := log.New(os.Stdout, "slack-bot: ", log.Lshortfile|log.LstdFlags)
	api := slack.New(config.SlackApiKey, slack.OptionLog(logger), slack.OptionDebug(false))

	rtm := api.NewRTM()
	go rtm.ManageConnection()

	for msg := range rtm.IncomingEvents {
		switch ev := msg.Data.(type) {
		case *slack.MessageEvent:
			// Untested. Bot was posting as me, and I couldn't exactly filter me out
			if (ev.User == "mtgbot") {
				break;
			}
			response := processMessage(ev.Text)
			if strings.Trim(response, "\n") != "" {
				params := slack.PostMessageParameters{
					AsUser:      true,
					UnfurlLinks: true,
					UnfurlMedia: true,
				}
				api.SendMessage(ev.Channel, slack.MsgOptionText(response, false), slack.MsgOptionPostMessageParameters(params))
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
		loadConfig("/home/mumbler/go/src/mtgbot-golang/config.json")
	}

	if config.CustomResponseFile != "" {
		fmt.Printf("Loading custom responses from '%s'\n", config.CustomResponseFile)
		loadCustomResponses()
	}
	slackStuff()
}
