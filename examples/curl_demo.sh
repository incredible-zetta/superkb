#!/usr/bin/env bash
#
# Sample curl walkthrough for the superkb API.
#
# Usage:
#   ./examples/curl_demo.sh                 # run the full flow
#   BASE=http://localhost:8080 ./examples/curl_demo.sh
#
# Requires: curl, jq (or python3 for parsing). Auth + port come from env with
# sensible defaults matching .env.example.

set -euo pipefail

BASE="${BASE:-http://localhost:8080}"
USER="${AUTH_USERNAME:-superkb}"
PASS="${AUTH_PASSWORD:-change-me}"
AUTH=(-u "${USER}:${PASS}")
API="${BASE}/api/v1"

# A file to upload. Override with FILE=/path/to/doc.pdf
FILE="${FILE:-}"

jqr() { python3 -c 'import sys,json;print(json.load(sys.stdin)["'"$1"'"])'; }

echo "== 0. Health (no auth) =="
curl -s "${BASE}/healthz"; echo

echo
echo "== 1. Upload a document =="
if [ -n "${FILE}" ]; then
  echo "   multipart upload: ${FILE}"
  DOC=$(curl -s "${AUTH[@]}" -X POST "${API}/documents" \
    -F "file=@${FILE}" \
    -F 'title=Sample Doc' \
    -F 'metadata={"source":"demo"}')
else
  echo "   JSON upload (plain text)"
  DOC=$(curl -s "${AUTH[@]}" -X POST "${API}/documents" \
    -H 'Content-Type: application/json' \
    -d '{
          "title":"HR Handbook",
          "content":"Employees get 20 days of paid vacation per year. Sick leave is 10 days. The CEO is Jane Smith.",
          "metadata":{"dept":"hr"}
        }')
fi
echo "${DOC}" | python3 -m json.tool
DOC_ID=$(echo "${DOC}" | jqr id)

echo
echo "== 2. List documents =="
curl -s "${AUTH[@]}" "${API}/documents?limit=5" | python3 -m json.tool

echo
echo "== 3. Create a knowledge base =="
KB=$(curl -s "${AUTH[@]}" -X POST "${API}/knowledge-bases" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Demo KB","description":"curl demo"}')
echo "${KB}" | python3 -m json.tool
KB_ID=$(echo "${KB}" | jqr id)

echo
echo "== 4. Add the document to the KB =="
curl -s "${AUTH[@]}" -o /dev/null -w "   HTTP %{http_code}\n" \
  -X PUT "${API}/knowledge-bases/${KB_ID}/documents/${DOC_ID}"

echo
echo "== 5. Build a RAG snapshot (async; returns a pending build) =="
BUILD=$(curl -s "${AUTH[@]}" -X POST "${API}/knowledge-bases/${KB_ID}/builds")
echo "${BUILD}" | python3 -m json.tool
BUILD_ID=$(echo "${BUILD}" | jqr id)

echo
echo "== 6. Poll build status until ready =="
for i in $(seq 1 120); do
  STATUS=$(curl -s "${AUTH[@]}" "${API}/knowledge-bases/${KB_ID}/builds" \
    | python3 -c "import sys,json;print([b for b in json.load(sys.stdin)['builds'] if b['id']=='${BUILD_ID}'][0]['status'])")
  echo "   [${i}] ${STATUS}"
  [ "${STATUS}" = "ready" ] && break
  if [ "${STATUS}" = "failed" ]; then
    curl -s "${AUTH[@]}" "${API}/knowledge-bases/${KB_ID}/builds" \
      | python3 -c "import sys,json;print('   error:',[b for b in json.load(sys.stdin)['builds'] if b['id']=='${BUILD_ID}'][0]['error'])"
    exit 1
  fi
  sleep 5
done

echo
echo "== 7. Enable the build for search =="
curl -s "${AUTH[@]}" -X POST "${API}/knowledge-bases/${KB_ID}/enable" \
  -H 'Content-Type: application/json' \
  -d "{\"build_id\":\"${BUILD_ID}\"}" | python3 -m json.tool

echo
echo "== 8. Search =="
curl -s "${AUTH[@]}" -X POST "${API}/knowledge-bases/${KB_ID}/search" \
  -H 'Content-Type: application/json' \
  -d '{"query":"who is the CEO and how many vacation days","top_k":5}' \
  | python3 -m json.tool

echo
echo "== 9. List builds (for rollback) =="
curl -s "${AUTH[@]}" "${API}/knowledge-bases/${KB_ID}/builds" | python3 -m json.tool

cat <<EOF

Done.

To roll back to a previous ready build, just enable it again:
  curl ${AUTH[*]} -X POST ${API}/knowledge-bases/${KB_ID}/enable \\
    -H 'Content-Type: application/json' \\
    -d '{"build_id":"<PREVIOUS_BUILD_ID>"}'

Other operations:
  Get a KB:        curl ${AUTH[*]} ${API}/knowledge-bases/${KB_ID}
  Disable search:  curl ${AUTH[*]} -X POST ${API}/knowledge-bases/${KB_ID}/disable
  Remove a doc:    curl ${AUTH[*]} -X DELETE ${API}/knowledge-bases/${KB_ID}/documents/${DOC_ID}
  Delete a doc:    curl ${AUTH[*]} -X DELETE ${API}/documents/${DOC_ID}
EOF
