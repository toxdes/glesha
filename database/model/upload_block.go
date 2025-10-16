package model

const CREATE_UPLOAD_BLOCKS_TABLE = `CREATE TABLE IF NOT EXISTS upload_blocks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			
			upload_id TEXT NOT NULL,

			block_number INTEGER NOT NULL,
			file_offset INTEGER NOT NULL,
			size INTEGER NOT NULL,

			status TEXT NOT NULL DEFAULT "QUEUED",
			etag TEXT,
			checksum TEXT,
			uploaded_at TEXT,
			error_message TEXT,

			UNIQUE(upload_id, block_number),
			FOREIGN KEY(upload_id) REFERENCES uploads(id) ON DELETE CASCADE
);`
