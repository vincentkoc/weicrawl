package schema

const Version = 1

const SQL = `
create table if not exists profiles (
  profile_id text primary key,
  wxid text,
  display_name text,
  source_root text not null,
  app_version text,
  raw_json text not null,
  updated_at text not null
);

create table if not exists contacts (
  profile_id text not null,
  contact_id text not null,
  alias text,
  display_name text,
  remark_name text,
  kind text not null,
  avatar_ref text,
  raw_json text not null,
  updated_at text not null,
  primary key (profile_id, contact_id)
);

create table if not exists chats (
  profile_id text not null,
  chat_id text not null,
  kind text not null,
  title text,
  last_message_at text,
  unread_count integer not null default 0,
  muted integer not null default 0,
  pinned integer not null default 0,
  raw_json text not null,
  updated_at text not null,
  primary key (profile_id, chat_id)
);

create table if not exists chat_members (
  profile_id text not null,
  chat_id text not null,
  contact_id text not null,
  display_name text,
  raw_json text not null default '{}',
  updated_at text not null,
  primary key (profile_id, chat_id, contact_id)
);

create table if not exists messages (
  profile_id text not null,
  message_id text not null,
  chat_id text not null,
  sender_id text,
  direction text not null,
  message_type text not null,
  sent_at text,
  edited_at text,
  deleted_at text,
  text text not null default '',
  normalized_text text not null default '',
  source_db text not null,
  source_rowid text not null,
  raw_json text not null,
  updated_at text not null,
  primary key (profile_id, message_id)
);

create table if not exists message_parts (
  profile_id text not null,
  message_id text not null,
  part_index integer not null,
  kind text not null,
  text text not null default '',
  media_id text,
  url text,
  raw_json text not null,
  primary key (profile_id, message_id, part_index)
);

create table if not exists message_events (
  event_id integer primary key autoincrement,
  profile_id text not null,
  chat_id text not null,
  message_id text not null,
  event_type text not null,
  event_at text not null,
  payload_json text not null
);

create unique index if not exists message_events_identity on message_events(
  profile_id,
  chat_id,
  message_id,
  event_type,
  event_at,
  payload_json
);

create table if not exists media_items (
  profile_id text not null,
  media_id text not null,
  kind text not null,
  source_path text,
  archive_path text,
  mime_type text,
  byte_size integer,
  sha256 text,
  width integer,
  height integer,
  duration_ms integer,
  raw_json text not null,
  updated_at text not null,
  primary key (profile_id, media_id)
);

create table if not exists favorites (
  profile_id text not null,
  favorite_id text not null,
  kind text not null,
  title text,
  text text not null default '',
  source_ref text,
  raw_json text not null,
  updated_at text not null,
  primary key (profile_id, favorite_id)
);

create table if not exists biz_accounts (
  profile_id text not null,
  account_id text not null,
  display_name text,
  raw_json text not null,
  updated_at text not null,
  primary key (profile_id, account_id)
);

create table if not exists biz_articles (
  profile_id text not null,
  article_id text not null,
  account_id text,
  title text not null default '',
  url text,
  summary text not null default '',
  published_at text,
  raw_json text not null,
  updated_at text not null,
  primary key (profile_id, article_id)
);

create table if not exists moments (
  profile_id text not null,
  moment_id text not null,
  author_id text,
  text text not null default '',
  created_at text,
  raw_json text not null,
  updated_at text not null,
  primary key (profile_id, moment_id)
);

create table if not exists raw_records (
  id integer primary key autoincrement,
  profile_id text not null,
  source_name text not null,
  source_table text not null,
  source_key text not null,
  record_kind text not null,
  payload_json text not null,
  observed_at text not null
);

create table if not exists sync_runs (
  run_id text primary key,
  source text not null,
  profile_id text,
  started_at text not null,
  finished_at text,
  status text not null,
  app_version text,
  source_root text,
  snapshot_path text,
  source_db_count integer not null default 0,
  imported_profiles integer not null default 0,
  imported_contacts integer not null default 0,
  imported_chats integer not null default 0,
  imported_messages integer not null default 0,
  imported_message_parts integer not null default 0,
  imported_message_events integer not null default 0,
  imported_media integer not null default 0,
  imported_biz_accounts integer not null default 0,
  imported_articles integer not null default 0,
  imported_favorites integer not null default 0,
  imported_moments integer not null default 0,
  imported_raw_records integer not null default 0,
  warnings_json text not null default '[]'
);

create table if not exists sync_state (
  source_name text not null,
  entity_type text not null,
  entity_id text not null,
  value text not null,
  updated_at text not null,
  primary key (source_name, entity_type, entity_id)
);

create virtual table if not exists message_fts using fts5(
  profile_id unindexed,
  message_id unindexed,
  chat_id unindexed,
  sender_id unindexed,
  body,
  tokenize='unicode61'
);

create virtual table if not exists article_fts using fts5(
  profile_id unindexed,
  article_id unindexed,
  account_id unindexed,
  body,
  tokenize='unicode61'
);

create index if not exists idx_messages_chat_time on messages(profile_id, chat_id, sent_at);
create index if not exists idx_messages_sender on messages(profile_id, sender_id);
create index if not exists idx_raw_records_source on raw_records(profile_id, source_name, source_table);
create index if not exists idx_sync_runs_started on sync_runs(started_at);
`

var SnapshotTables = []string{
	"profiles",
	"contacts",
	"chats",
	"chat_members",
	"messages",
	"message_parts",
	"message_events",
	"media_items",
	"favorites",
	"biz_accounts",
	"biz_articles",
	"moments",
	"raw_records",
	"sync_runs",
	"sync_state",
}
