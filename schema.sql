-- MySQL schema for King of the Table persistence
-- Engine/charset
SET NAMES utf8mb4;
SET time_zone = '+00:00';

-- Players catalog
CREATE TABLE IF NOT EXISTS players (
  id INT UNSIGNED NOT NULL AUTO_INCREMENT,
  name VARCHAR(100) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_seen DATETIME NULL,
  wins INT UNSIGNED NOT NULL DEFAULT 0,
  survives INT UNSIGNED NOT NULL DEFAULT 0,
  full_rotation INT UNSIGNED NOT NULL DEFAULT 0,
  PRIMARY KEY (id),
  UNIQUE KEY ux_players_name (name),
  KEY ix_players_wins (wins),
  KEY ix_players_survives (survives),
  KEY ix_players_last_seen (last_seen)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Games catalog (server-generated hex IDs)
CREATE TABLE IF NOT EXISTS games (
  id CHAR(24) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Goal events, capturing rotation outcome for auditing/statistics
CREATE TABLE IF NOT EXISTS goal_events (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  game_id CHAR(24) NOT NULL,
  scoring_team ENUM('red','blue') NOT NULL,
  red_forward_id INT UNSIGNED NOT NULL,
  red_goalkeeper_id INT UNSIGNED NOT NULL,
  blue_forward_id INT UNSIGNED NOT NULL,
  blue_goalkeeper_id INT UNSIGNED NOT NULL,
  benched_player_id INT UNSIGNED NOT NULL,
  moved_to_goalkeeper_id INT UNSIGNED NOT NULL,
  new_forward_id INT UNSIGNED NOT NULL,
  full_rotation TINYINT(1) NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY ix_goal_events_game (game_id, created_at),
  CONSTRAINT fk_ge_game FOREIGN KEY (game_id) REFERENCES games(id)
    ON DELETE CASCADE ON UPDATE RESTRICT,
  CONSTRAINT fk_ge_red_f FOREIGN KEY (red_forward_id) REFERENCES players(id),
  CONSTRAINT fk_ge_red_g FOREIGN KEY (red_goalkeeper_id) REFERENCES players(id),
  CONSTRAINT fk_ge_blue_f FOREIGN KEY (blue_forward_id) REFERENCES players(id),
  CONSTRAINT fk_ge_blue_g FOREIGN KEY (blue_goalkeeper_id) REFERENCES players(id),
  CONSTRAINT fk_ge_benched FOREIGN KEY (benched_player_id) REFERENCES players(id),
  CONSTRAINT fk_ge_moved FOREIGN KEY (moved_to_goalkeeper_id) REFERENCES players(id),
  CONSTRAINT fk_ge_newf FOREIGN KEY (new_forward_id) REFERENCES players(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Optional helper view for leaderboard
-- Aggregates are already denormalized in players, but this can be handy if using only events
-- CREATE VIEW leaderboard AS
-- SELECT p.id, p.name,
--        SUM(CASE WHEN e.scoring_team IS NOT NULL AND (p.id IN (e.red_forward_id, e.red_goalkeeper_id) AND e.scoring_team='red') OR (p.id IN (e.blue_forward_id, e.blue_goalkeeper_id) AND e.scoring_team='blue') THEN 1 ELSE 0 END) AS wins,
--        SUM(CASE WHEN p.id IN (e.red_forward_id, e.red_goalkeeper_id, e.blue_forward_id, e.blue_goalkeeper_id) AND p.id <> e.benched_player_id THEN 1 ELSE 0 END) AS survives
-- FROM players p
-- JOIN goal_events e ON p.id IN (e.red_forward_id, e.red_goalkeeper_id, e.blue_forward_id, e.blue_goalkeeper_id)
-- GROUP BY p.id, p.name;
