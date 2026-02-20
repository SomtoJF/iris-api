package redispubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// ActionType represents the type of action being sent
type ActionType string

const (
	ActionTriggerUpdate                ActionType = "TRIGGER_UPDATE"
	ActionAccountUpdate                ActionType = "ACCOUNT_UPDATE"
	ActionSequenceUpdate               ActionType = "SEQUENCE_UPDATE"
	ActionSequenceGenerationCompletion ActionType = "SEQUENCE_GENERATION_COMPLETION"
	ActionKeyPersonUpdate              ActionType = "KEY_PERSON_UPDATE"
	ActionCampaignUpdate               ActionType = "CAMPAIGN_UPDATE"
	ActionWorkflowUpdate               ActionType = "WORKFLOW_UPDATE"
	ActionNotification                 ActionType = "NOTIFICATION"
	ActionSystemMessage                ActionType = "SYSTEM_MESSAGE"
	ActionKeyPeopleActivityCreated     ActionType = "KEY_PEOPLE_ACTIVITY_CREATED"
	ActionEmailSent                    ActionType = "EMAIL_MESSAGE_SENT"
	ActionEmailFailed                  ActionType = "EMAIL_MESSAGE_FAILED"
	ActionSequenceEmailSent            ActionType = "SEQUENCE_EMAIL_SENT"
	ActionSequenceEmailFailed          ActionType = "SEQUENCE_EMAIL_FAILED"
	ActionInboxChanged                 ActionType = "INBOX_CHANGED"
	ActionBotWorkflowStarted           ActionType = "BOT_WORKFLOW_STARTED"
	ActionBotTaskProgress              ActionType = "BOT_TASK_PROGRESS"
	ActionBotTaskCompleted             ActionType = "BOT_TASK_COMPLETED"
	ActionBotTaskFailed                ActionType = "BOT_TASK_FAILED"
	ActionBotWorkflowCompleted         ActionType = "BOT_WORKFLOW_COMPLETED"
	ActionAgentToolReady               ActionType = "AGENT_TOOL_READY"
	ActionAgentCompleted               ActionType = "AGENT_COMPLETED"
	ActionAgentFailed                  ActionType = "AGENT_FAILED"
)

// Event represents a real-time event to be sent to clients
type Event struct {
	Action ActionType  `json:"action"`
	Data   interface{} `json:"data"`
}

// RedisPubSub handles Redis publish/subscribe operations
type RedisPubSub struct {
	client *redis.Client
}

// NewRedisPubSub creates a new RedisPubSub instance
func NewRedisPubSub(client *redis.Client) *RedisPubSub {
	return &RedisPubSub{
		client: client,
	}
}

// GetUserChannel returns the Redis channel name for a specific user
func (r *RedisPubSub) GetUserChannel(userID string) string {
	return fmt.Sprintf("user:%s:events", userID)
}

// PublishToUser publishes an event to a specific user's channel
func (r *RedisPubSub) PublishToUser(ctx context.Context, userID string, action ActionType, data interface{}) error {
	event := Event{
		Action: action,
		Data:   data,
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	channel := r.GetUserChannel(userID)
	err = r.client.Publish(ctx, channel, string(eventJSON)).Err()
	if err != nil {
		return fmt.Errorf("failed to publish to Redis channel %s: %w", channel, err)
	}

	log.Printf("Published event to channel %s: %s", channel, string(eventJSON))
	return nil
}

// SubscribeToUser subscribes to a user's channel and returns a channel for receiving messages
func (r *RedisPubSub) SubscribeToUser(ctx context.Context, userID string) (<-chan Event, error) {
	channel := r.GetUserChannel(userID)
	pubsub := r.client.Subscribe(ctx, channel)

	// Test subscription
	_, err := pubsub.Receive(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to channel %s: %w", channel, err)
	}

	eventChan := make(chan Event)

	go func() {
		defer close(eventChan)
		defer pubsub.Close()

		ch := pubsub.Channel()
		for {
			select {
			case msg := <-ch:
				if msg == nil {
					return
				}

				var event Event
				if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
					log.Printf("Failed to unmarshal event from channel %s: %v", channel, err)
					continue
				}

				select {
				case eventChan <- event:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	log.Printf("Subscribed to channel %s", channel)
	return eventChan, nil
}

// SubscribeToChannel subscribes to a specific channel and returns a channel for receiving messages
func (r *RedisPubSub) SubscribeToChannel(ctx context.Context, channel string) (<-chan Event, error) {
	pubsub := r.client.Subscribe(ctx, channel)

	// Test subscription
	_, err := pubsub.Receive(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to channel %s: %w", channel, err)
	}

	eventChan := make(chan Event)

	go func() {
		defer close(eventChan)
		defer pubsub.Close()

		ch := pubsub.Channel()
		for {
			select {
			case msg := <-ch:
				if msg == nil {
					return
				}

				var event Event
				if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
					log.Printf("Failed to unmarshal event from channel %s: %v", channel, err)
					continue
				}

				select {
				case eventChan <- event:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	log.Printf("Subscribed to channel %s", channel)
	return eventChan, nil
}

// PublishToChannel publishes an event to a specific channel
func (r *RedisPubSub) PublishToChannel(ctx context.Context, channel string, action ActionType, data interface{}) error {
	event := Event{
		Action: action,
		Data:   data,
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	err = r.client.Publish(ctx, channel, string(eventJSON)).Err()
	if err != nil {
		return fmt.Errorf("failed to publish to Redis channel %s: %w", channel, err)
	}

	log.Printf("Published event to channel %s: %s", channel, string(eventJSON))
	return nil
}

// PublishToAllUsers publishes an event to all active user channels
func (r *RedisPubSub) PublishToAllUsers(ctx context.Context, action ActionType, data interface{}) error {
	// Get all user channels pattern
	pattern := "user:*:events"
	keys, err := r.client.Keys(ctx, pattern).Result()
	if err != nil {
		return fmt.Errorf("failed to get user channels: %w", err)
	}

	event := Event{
		Action: action,
		Data:   data,
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// Publish to all user channels
	for _, channel := range keys {
		err = r.client.Publish(ctx, channel, string(eventJSON)).Err()
		if err != nil {
			log.Printf("Failed to publish to channel %s: %v", channel, err)
		}
	}

	log.Printf("Published event to %d user channels", len(keys))
	return nil
}

// SetUserOnline marks a user as online by setting a key with TTL
func (r *RedisPubSub) SetUserOnline(ctx context.Context, userID string) error {
	key := fmt.Sprintf("user:%s:online", userID)
	err := r.client.Set(ctx, key, "1", 30*time.Second).Err()
	if err != nil {
		return fmt.Errorf("failed to set user online status: %w", err)
	}
	return nil
}

// SetUserOffline removes the user's online status
func (r *RedisPubSub) SetUserOffline(ctx context.Context, userID string) error {
	key := fmt.Sprintf("user:%s:online", userID)
	err := r.client.Del(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("failed to remove user online status: %w", err)
	}
	return nil
}

// IsUserOnline checks if a user is currently online
func (r *RedisPubSub) IsUserOnline(ctx context.Context, userID string) (bool, error) {
	key := fmt.Sprintf("user:%s:online", userID)
	exists, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check user online status: %w", err)
	}
	return exists > 0, nil
}

// RedisRateLimiter handles rate limiting functionality using Redis
type RedisRateLimiter struct {
	client *redis.Client
}

// NewRedisRateLimiter creates a new RedisRateLimiter instance
func NewRedisRateLimiter(client *redis.Client) *RedisRateLimiter {
	return &RedisRateLimiter{
		client: client,
	}
}

// GetActiveConnectionsKey returns the Redis key for tracking active connections
func (r *RedisRateLimiter) GetActiveConnectionsKey(provider string, keyIndex int) string {
	return fmt.Sprintf("%s:active_connections:%d", provider, keyIndex)
}

// TryAcquireSlot attempts to acquire a connection slot for the given key
// Returns true if successful, false if rate limit is exceeded
func (r *RedisRateLimiter) TryAcquireSlot(ctx context.Context, provider string, keyIndex int, maxConcurrent int, ttl time.Duration) (bool, error) {
	activeKey := r.GetActiveConnectionsKey(provider, keyIndex)

	// Improved Lua script that handles TTL properly
	script := `
		local current = redis.call('GET', KEYS[1])
		if current == false then
			current = 0
		else
			current = tonumber(current)
		end
		
		if current < tonumber(ARGV[1]) then
			local newValue = redis.call('INCR', KEYS[1])
			-- Only set TTL if this is the first increment (newValue == 1)
			if newValue == 1 then
				redis.call('EXPIRE', KEYS[1], ARGV[2])
			end
			return 1
		else
			return 0
		end
	`

	result, err := r.client.Eval(ctx, script, []string{activeKey}, maxConcurrent, int(ttl.Seconds())).Result()
	if err != nil {
		return false, fmt.Errorf("failed to execute rate limit script: %w", err)
	}

	return result.(int64) == 1, nil
}

// ReleaseSlot decrements the active connections counter for the given key
func (r *RedisRateLimiter) ReleaseSlot(ctx context.Context, provider string, keyIndex int) error {
	activeKey := r.GetActiveConnectionsKey(provider, keyIndex)

	_, err := r.client.Decr(ctx, activeKey).Result()
	if err != nil {
		return fmt.Errorf("failed to decrement active connections: %w", err)
	}

	return nil
}

// GetActiveSlots returns the current number of active slots for the given key
func (r *RedisRateLimiter) GetActiveSlots(ctx context.Context, provider string, keyIndex int) (int, error) {
	activeKey := r.GetActiveConnectionsKey(provider, keyIndex)

	val, err := r.client.Get(ctx, activeKey).Result()
	if err != nil {
		if err == redis.Nil {
			return 0, nil // Key doesn't exist, so 0 active connections
		}
		return 0, fmt.Errorf("failed to get active connections: %w", err)
	}

	var count int
	if _, err := fmt.Sscanf(val, "%d", &count); err != nil {
		return 0, fmt.Errorf("failed to parse active connections count: %w", err)
	}

	return count, nil
}
