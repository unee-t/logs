aws --profile uneet-prod logs filter-log-events --log-group-name "/aws/lambda/alambda_simple" --start-time $(date -d "-8 hours" +%s000) \
	--filter-pattern '{ $.level = "error" }' |
	jq '.events[].message|fromjson'
