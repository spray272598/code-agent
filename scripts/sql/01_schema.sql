-- Code-Agent schema (draft, aligns with docs/design.md)
CREATE DATABASE IF NOT EXISTS `code_agent` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE `code_agent`;

CREATE TABLE IF NOT EXISTS `user_account` (
  `id`            VARCHAR(64)  NOT NULL,
  `name`          VARCHAR(128) NOT NULL DEFAULT '',
  `api_key_hash`  VARCHAR(128) NOT NULL DEFAULT '',
  `status`        VARCHAR(32)  NOT NULL DEFAULT 'ACTIVE',
  `created_at`    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at`    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  KEY `idx_api_key_hash` (`api_key_hash`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户';

CREATE TABLE IF NOT EXISTS `chat_session` (
  `id`            VARCHAR(64)  NOT NULL,
  `user_id`       VARCHAR(64)  NOT NULL,
  `project_id`    VARCHAR(128) NOT NULL DEFAULT '',
  `agent_id`      VARCHAR(64)  NOT NULL DEFAULT 'code-agent',
  `title`         VARCHAR(255) NOT NULL DEFAULT '',
  `status`        VARCHAR(32)  NOT NULL DEFAULT 'ACTIVE',
  `message_count` INT          NOT NULL DEFAULT 0,
  `token_used`    INT          NOT NULL DEFAULT 0,
  `working_dir`   VARCHAR(512) NOT NULL DEFAULT '',
  `metadata_json` JSON         NULL,
  `created_at`    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at`    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  KEY `idx_user_status` (`user_id`, `status`),
  KEY `idx_project` (`project_id`),
  KEY `idx_updated` (`updated_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='会话';

CREATE TABLE IF NOT EXISTS `chat_message` (
  `id`           VARCHAR(64)  NOT NULL,
  `session_id`   VARCHAR(64)  NOT NULL,
  `role`         VARCHAR(32)  NOT NULL,
  `content`      MEDIUMTEXT   NOT NULL,
  `tool_name`    VARCHAR(128) NOT NULL DEFAULT '',
  `tool_call_id` VARCHAR(128) NOT NULL DEFAULT '',
  `step`         INT          NOT NULL DEFAULT 0,
  `token_count`  INT          NOT NULL DEFAULT 0,
  `priority`     INT          NOT NULL DEFAULT 1,
  `created_at`   DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  KEY `idx_session_created` (`session_id`, `created_at`),
  CONSTRAINT `fk_msg_session` FOREIGN KEY (`session_id`) REFERENCES `chat_session` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='消息';

CREATE TABLE IF NOT EXISTS `chat_milestone` (
  `id`         BIGINT       NOT NULL AUTO_INCREMENT,
  `session_id` VARCHAR(64)  NOT NULL,
  `type`       VARCHAR(64)  NOT NULL,
  `content`    VARCHAR(1024) NOT NULL DEFAULT '',
  `step`       INT          NOT NULL DEFAULT 0,
  `created_at` DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  KEY `idx_session_created` (`session_id`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='里程碑';

CREATE TABLE IF NOT EXISTS `session_summary` (
  `id`         BIGINT       NOT NULL AUTO_INCREMENT,
  `session_id` VARCHAR(64)  NOT NULL,
  `summary`    MEDIUMTEXT   NOT NULL,
  `token_est`  INT          NOT NULL DEFAULT 0,
  `version`    INT          NOT NULL DEFAULT 1,
  `created_at` DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at` DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_session` (`session_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='会话摘要（压缩产物）';

CREATE TABLE IF NOT EXISTS `core_memory` (
  `id`         BIGINT       NOT NULL AUTO_INCREMENT,
  `user_id`    VARCHAR(64)  NOT NULL,
  `project_id` VARCHAR(128) NOT NULL DEFAULT '',
  `scope`      VARCHAR(32)  NOT NULL DEFAULT 'user' COMMENT 'user|project',
  `category`   VARCHAR(64)  NOT NULL DEFAULT 'general',
  `content`    TEXT         NOT NULL,
  `importance` INT          NOT NULL DEFAULT 50,
  `source`     VARCHAR(64)  NOT NULL DEFAULT '',
  `embedding`  MEDIUMTEXT   NULL COMMENT 'JSON-encoded float32 vector for semantic search',
  `created_at` DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at` DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  KEY `idx_user_scope` (`user_id`, `scope`),
  KEY `idx_project` (`project_id`),
  KEY `idx_importance` (`importance`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='长期记忆';

ALTER TABLE `core_memory` ADD COLUMN `embedding` MEDIUMTEXT NULL COMMENT 'JSON-encoded float32 vector';

CREATE TABLE IF NOT EXISTS `mcp_server_config` (
  `id`           BIGINT       NOT NULL AUTO_INCREMENT,
  `name`         VARCHAR(64)  NOT NULL,
  `user_id`      VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '空=全局',
  `transport`    VARCHAR(32)  NOT NULL COMMENT 'stdio|sse|http',
  `command`      VARCHAR(512) NOT NULL DEFAULT '',
  `args_json`    JSON         NULL,
  `env_json`     JSON         NULL,
  `url`          VARCHAR(1024) NOT NULL DEFAULT '',
  `enabled`      TINYINT(1)   NOT NULL DEFAULT 1,
  `timeout_sec`  INT          NOT NULL DEFAULT 60,
  `created_at`   DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at`   DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_name_user` (`name`, `user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='MCP 服务配置';

CREATE TABLE IF NOT EXISTS `audit_log` (
  `id`         BIGINT       NOT NULL AUTO_INCREMENT,
  `user_id`    VARCHAR(64)  NOT NULL DEFAULT '',
  `session_id` VARCHAR(64)  NOT NULL DEFAULT '',
  `action`     VARCHAR(64)  NOT NULL,
  `tool`       VARCHAR(128) NOT NULL DEFAULT '',
  `detail`     TEXT         NULL,
  `decision`   VARCHAR(32)  NOT NULL DEFAULT '',
  `created_at` DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  KEY `idx_session` (`session_id`),
  KEY `idx_user_time` (`user_id`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='审计';

CREATE TABLE IF NOT EXISTS `object_meta` (
  `id`         BIGINT       NOT NULL AUTO_INCREMENT,
  `user_id`    VARCHAR(64)  NOT NULL DEFAULT '',
  `session_id` VARCHAR(64)  NOT NULL DEFAULT '',
  `object_key` VARCHAR(512) NOT NULL,
  `size`       BIGINT       NOT NULL DEFAULT 0,
  `content_type` VARCHAR(128) NOT NULL DEFAULT 'text/plain',
  `created_at` DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_object_key` (`object_key`),
  KEY `idx_session` (`session_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='对象存储元数据';

CREATE TABLE IF NOT EXISTS `ssh_connection` (
  `id`               VARCHAR(64)  NOT NULL,
  `name`             VARCHAR(128) NOT NULL,
  `host`             VARCHAR(256) NOT NULL,
  `port`             INT          NOT NULL DEFAULT 22,
  `username`         VARCHAR(128) NOT NULL,
  `auth_type`        VARCHAR(32)  NOT NULL DEFAULT 'password' COMMENT 'password|private_key',
  `password`         VARCHAR(512) NOT NULL DEFAULT '',
  `private_key`      TEXT         NULL,
  `enabled`          TINYINT(1)   NOT NULL DEFAULT 1,
  `last_connected_at` DATETIME(3) NULL,
  `created_at`       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at`       DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='SSH远程连接配置';
