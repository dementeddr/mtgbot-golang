# mtgbot-golang
Rewrite of mtgbot in Go
DEPENDENCIES: https://github.com/nlopes/slack

Currently only supports using api.magicthegathering.io as the card fetching api

Config file path can be passed in a command line argument, defaults to ./config.json

Custom responses consist of an array of trigger keywords and response arrays.  More than one entry in the response array, and a value will be picked at random.
Triggers use golang regex to parse incoming messages

To use:
create a slack bot and put its api key in config.json
run the bot

NOTE: Modifications of config.json or custom.json will require restarts of the bot to take effect
