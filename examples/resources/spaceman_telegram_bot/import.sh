# The bot is a singleton: its import ID is always telegram-bot.
terraform import spaceman_telegram_bot.this telegram-bot
# Import leaves bot_token_wo_version unset: the first apply after it resends the token (token_version +1, the webhook
# secret rotates). It is the same bot, so Telegram links stay.
