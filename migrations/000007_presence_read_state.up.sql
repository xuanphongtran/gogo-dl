CREATE TABLE room_read_states (
    room_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    last_read_message_id BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (room_id, user_id),
    FOREIGN KEY (room_id, user_id) REFERENCES room_members (room_id, user_id) ON DELETE CASCADE,
    CONSTRAINT room_read_states_cursor_nonnegative CHECK (last_read_message_id >= 0)
);
