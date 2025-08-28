#!/bin/bash

# Test script for bidirectional video calling functionality
# This script tests the Agora token generation and call setup endpoints

echo "Testing Bidirectional Video Call Setup..."
echo "========================================="

# Configuration
SERVER_URL="http://localhost:8080"
CHANNEL="test-channel-$(date +%s)"
CALLER_ID="user1"
CALLEE_ID="user2"

echo "Channel: $CHANNEL"
echo "Caller ID: $CALLER_ID"
echo "Callee ID: $CALLEE_ID"
echo ""

# Test 1: Individual token generation for caller
echo "1. Testing individual token generation for caller..."
curl -s -X GET "$SERVER_URL/api/agora-token/?channel=$CHANNEL&uid=$CALLER_ID" \
  -H "Content-Type: application/json" | jq .

echo ""

# Test 2: Individual token generation for callee
echo "2. Testing individual token generation for callee..."
curl -s -X GET "$SERVER_URL/api/agora-token/?channel=$CHANNEL&uid=$CALLEE_ID" \
  -H "Content-Type: application/json" | jq .

echo ""

# Test 3: Bidirectional call setup (new endpoint)
echo "3. Testing bidirectional call setup endpoint..."
curl -s -X POST "$SERVER_URL/api/setup-call/" \
  -H "Content-Type: application/json" \
  -d "{
    \"channel\": \"$CHANNEL\",
    \"callerId\": \"$CALLER_ID\",
    \"calleeId\": \"$CALLEE_ID\"
  }" | jq .

echo ""

# Test 4: Verify both tokens are publisher role
echo "4. Verification Summary:"
echo "- Both users should receive 'publisher' role tokens"
echo "- This allows bidirectional video and audio"
echo "- Tokens should have the same channel but different UIDs"
echo "- appId should be consistent across all responses"

echo ""
echo "Testing completed!"
echo ""
echo "Manual Testing Instructions:"
echo "1. Start two clients (web browsers or mobile apps)"
echo "2. User1 initiates a call to User2"
echo "3. User2 accepts the call"
echo "4. Both users should be able to:"
echo "   - See each other's video"
echo "   - Hear each other's audio"
echo "   - Control their own camera/microphone"
echo ""
echo "WebSocket Testing:"
echo "Connect to: ws://localhost:8080/ws/"
echo "Send test messages:"
echo '{"type": "new-user-add", "userId": "user1"}'
echo '{"type": "agora-signal", "userId": "user1", "data": {"action": "call-request", "targetId": "user2", "channel": "'$CHANNEL'", "callType": "video"}}'