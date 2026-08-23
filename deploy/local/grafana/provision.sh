#!/bin/sh
# Install the DTHCMS datasource, dashboards, contact point and alert rules into the local
# Grafana, then verify that each one is actually there.
#
# This runs against Grafana's HTTP API rather than dropping files into the image's
# provisioning directory. The directory layout inside grafana/otel-lgtm is an
# implementation detail of that image: mounting over it can hide the dashboards it ships
# with, and a mount at a path the image does not read fails silently, leaving an empty
# Grafana that looks like a configuration mistake somewhere else entirely.
#
# Every call reports its HTTP status and prints Grafana's response on failure. An earlier
# version tolerated every error with `|| true` so that "already exists" would not stop a
# re-run — and it therefore reported complete success while three of four alert rules had
# failed to install. Tolerating one specific expected failure is fine; tolerating all of
# them means the script cannot tell you anything.
#
# Safe to re-run: everything is created or replaced.

set -eu

GRAFANA_URL="${GRAFANA_URL:-http://observability:3000}"
GRAFANA_AUTH="${GRAFANA_AUTH:-admin:admin}"
ALERT_EMAIL="${DTHCMS_ALERT_EMAIL:-amlan@dthcms.local}"
PROMETHEUS_URL="${PROMETHEUS_URL:-http://localhost:9090}"
DATASOURCE_UID="dthcms-prometheus"
FOLDER_UID="dthcms"

BODY_FILE=$(mktemp)
trap 'rm -f "$BODY_FILE"' EXIT

failures=0

say() { printf '  %s\n' "$*"; }

# api METHOD PATH [BODY] — sets RESPONSE and returns the HTTP status in STATUS.
api() {
	_method="$1"
	_path="$2"
	_body="${3:-}"

	if [ -n "$_body" ]; then
		STATUS=$(printf '%s' "$_body" | curl -sS -o "$BODY_FILE" -w '%{http_code}' \
			-u "$GRAFANA_AUTH" \
			-X "$_method" "$GRAFANA_URL$_path" \
			-H 'Content-Type: application/json' \
			-H 'X-Disable-Provenance: true' \
			--data-binary @- || echo 000)
	else
		STATUS=$(curl -sS -o "$BODY_FILE" -w '%{http_code}' \
			-u "$GRAFANA_AUTH" \
			-X "$_method" "$GRAFANA_URL$_path" \
			-H 'X-Disable-Provenance: true' || echo 000)
	fi

	RESPONSE=$(cat "$BODY_FILE")
}

# expect DESCRIPTION METHOD PATH [BODY] — fails loudly on anything but 2xx or 409.
expect() {
	_what="$1"
	shift
	api "$@"

	case "$STATUS" in
	2*)
		say "$_what"
		return 0
		;;
	409 | 412)
		# Already exists. The only failure worth tolerating, and only because re-running
		# this script must be safe.
		#
		# 412 as well as 409: Grafana 11 answers a folder that already exists with
		# "412 version-mismatch" rather than a conflict, which reads like a real error and
		# is not one.
		say "$_what (already present)"
		return 0
		;;
	*)
		say "FAILED: $_what"
		printf '    HTTP %s\n' "$STATUS" >&2
		printf '    %s\n' "$(printf '%s' "$RESPONSE" | head -c 600)" >&2
		failures=$((failures + 1))
		return 1
		;;
	esac
}

# ---------------------------------------------------------------------------
# Wait for Grafana
# ---------------------------------------------------------------------------

printf 'Waiting for Grafana at %s' "$GRAFANA_URL"
attempt=0
until curl -sS -f "$GRAFANA_URL/api/health" >/dev/null 2>&1; do
	attempt=$((attempt + 1))
	if [ "$attempt" -ge 60 ]; then
		printf '\n'
		echo "Grafana did not become healthy. Check: docker compose logs observability" >&2
		exit 1
	fi
	printf '.'
	sleep 2
done
printf '\n'

# ---------------------------------------------------------------------------
# Datasource
#
# A fixed UID rather than a discovered one. The dashboards and alert rules reference it
# directly, so they are complete, reviewable files that do not depend on whatever UID this
# image happened to generate at first start.
# ---------------------------------------------------------------------------

datasource_json=$(
	cat <<JSON
{
  "uid": "$DATASOURCE_UID",
  "name": "DTHCMS Prometheus",
  "type": "prometheus",
  "access": "proxy",
  "url": "$PROMETHEUS_URL",
  "isDefault": false,
  "jsonData": { "httpMethod": "POST", "timeInterval": "15s" }
}
JSON
)

api POST /api/datasources "$datasource_json"
case "$STATUS" in
2*) say "datasource $DATASOURCE_UID" ;;
409 | 412)
	# Exists already: update it, so a changed URL is picked up on a re-run.
	if expect "datasource $DATASOURCE_UID (updated)" PUT "/api/datasources/uid/$DATASOURCE_UID" "$datasource_json"; then :; fi
	;;
*)
	say "FAILED: datasource $DATASOURCE_UID"
	printf '    HTTP %s\n    %s\n' "$STATUS" "$RESPONSE" >&2
	failures=$((failures + 1))
	;;
esac

# ---------------------------------------------------------------------------
# Folder
# ---------------------------------------------------------------------------

expect "folder $FOLDER_UID" POST /api/folders \
	"{\"uid\":\"$FOLDER_UID\",\"title\":\"DTHCMS\"}" || true

# ---------------------------------------------------------------------------
# Dashboards
# ---------------------------------------------------------------------------

for file in /provision/dashboards/*.json; do
	name=$(basename "$file" .json)
	payload=$(
		printf '{"folderUid":"%s","overwrite":true,"message":"provisioned by DTHCMS","dashboard":' "$FOLDER_UID"
		cat "$file"
		printf '}'
	)
	expect "dashboard $name" POST /api/dashboards/db "$payload" || true
done

# ---------------------------------------------------------------------------
# Contact point
#
# Replaced rather than created blindly. Posting on every run would leave a second contact
# point with the same name and a different UID, and Grafana would then show two — which is
# how "delivered to" ends up reading as two addresses joined together.
# ---------------------------------------------------------------------------

contact=$(sed "s|__ALERT_EMAIL__|$ALERT_EMAIL|" /provision/alerting/contact-point.json)

# Fixed uid, like the dashboards and alert rules. Grafana assigns a random one when a
# contact point is created without it, so the first version of this script produced a new
# contact point on every run rather than replacing the old one - and two receivers with
# the same name means Grafana picks one and nobody can tell which.
api POST /api/v1/provisioning/contact-points "$contact"
case "$STATUS" in
2*) say "contact point dthcms-oncall -> $ALERT_EMAIL" ;;
409 | 412 | 500)
	# Already exists. Update it in place; the uid is ours, so this is unambiguous.
	expect "contact point dthcms-oncall -> $ALERT_EMAIL (updated)" \
		PUT /api/v1/provisioning/contact-points/dthcms-oncall "$contact" || true
	;;
*)
	say "FAILED: contact point dthcms-oncall"
	printf '    HTTP %s\n    %s\n' "$STATUS" "$RESPONSE" >&2
	failures=$((failures + 1))
	;;
esac

# Remove any same-named contact point left behind by an earlier run. Grafana refuses to
# delete a receiver a notification policy still references, so a failure here is reported
# and not fatal: the duplicate is untidy, not broken.
api GET /api/v1/provisioning/contact-points
strays=$(printf '%s' "$RESPONSE" |
	tr '{' '\n' |
	grep '"name" *: *"dthcms-oncall"' |
	sed -n 's/.*"uid" *: *"\([^"]*\)".*/\1/p' |
	grep -v '^dthcms-oncall$' || true)

for stray in $strays; do
	api DELETE "/api/v1/provisioning/contact-points/$stray"
	case "$STATUS" in
	2*) say "removed duplicate contact point $stray" ;;
	*) say "could not remove duplicate contact point $stray (HTTP $STATUS) - delete it in Grafana under Alerting > Contact points" ;;
	esac
done

expect "notification policy" PUT /api/v1/provisioning/policies \
	"$(cat /provision/alerting/notification-policy.json)" || true

# ---------------------------------------------------------------------------
# Alert rules
#
# One object at a time, extracted with sed rather than jq: the curl image has no jq, and
# adding a second tool to the local stack to reformat four rules is not worth it. The file
# is generated, so its shape is stable.
# ---------------------------------------------------------------------------

for uid in dthcms-error-rate dthcms-latency dthcms-db-pool dthcms-no-telemetry; do
	rule=$(sed -n "/\"uid\": \"$uid\"/,/^  }/p" /provision/alerting/rules.json |
		sed '1s/^/{/' | sed '$s/,$//')

	if [ -z "$rule" ]; then
		say "FAILED: alert rule $uid could not be read from rules.json"
		failures=$((failures + 1))
		continue
	fi

	# Delete first so a changed rule replaces the old one. A 404 here is normal.
	api DELETE "/api/v1/provisioning/alert-rules/$uid"

	expect "alert rule $uid" POST /api/v1/provisioning/alert-rules "$rule" || true
done

# ---------------------------------------------------------------------------
# Verify
#
# Each object is fetched by id. The previous version counted matches in one big JSON blob,
# which agreed with the installation step because both were wrong in the same direction.
# ---------------------------------------------------------------------------

echo ''
echo 'Verifying:'

verify_failures=0

check() {
	_what="$1"
	_path="$2"
	api GET "$_path"
	case "$STATUS" in
	2*) say "OK      $_what" ;;
	*)
		say "MISSING $_what (HTTP $STATUS)"
		verify_failures=$((verify_failures + 1))
		;;
	esac
}

for uid in dthcms-latency dthcms-errors dthcms-saturation; do
	check "dashboard $uid" "/api/dashboards/uid/$uid"
done

for uid in dthcms-error-rate dthcms-latency dthcms-db-pool dthcms-no-telemetry; do
	check "alert rule $uid" "/api/v1/provisioning/alert-rules/$uid"
done

api GET /api/v1/provisioning/contact-points
if printf '%s' "$RESPONSE" | grep -q 'dthcms-oncall'; then
	say "OK      contact point dthcms-oncall"
else
	say "MISSING contact point dthcms-oncall"
	verify_failures=$((verify_failures + 1))
fi

# ---------------------------------------------------------------------------

echo ''
total=$((failures + verify_failures))
if [ "$total" -gt 0 ]; then
	echo "Observability provisioning incomplete: $failures install failure(s), $verify_failures missing after install." >&2
	exit 1
fi

echo 'Observability ready.'
echo "  Grafana        http://localhost:${GRAFANA_PORT:-3001}  (DTHCMS folder)"
echo "  Alerts go to   $ALERT_EMAIL, visible in Mailpit at http://localhost:${MAILPIT_UI_PORT:-8025}"
