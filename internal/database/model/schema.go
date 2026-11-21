package model

const CatalogSchema = `
PRAGMA cache_size=-2048;
PRAGMA temp_store=FILE;
PRAGMA journal_mode=DELETE;
CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY,value TEXT NOT NULL);
INSERT OR IGNORE INTO meta VALUES('version','2');
CREATE TABLE IF NOT EXISTS snapshots(seq INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT UNIQUE,parent TEXT,status TEXT,data TEXT,baseline INTEGER NOT NULL DEFAULT 0,wrapper TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS nodes(id INTEGER PRIMARY KEY AUTOINCREMENT,parent INTEGER NOT NULL,name TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS node_children ON nodes(parent,name);
CREATE TABLE IF NOT EXISTS versions(id INTEGER PRIMARY KEY AUTOINCREMENT,kind TEXT,size INTEGER,mode INTEGER,mtime INTEGER,uid INTEGER,gid INTEGER,link TEXT,hash BLOB,pax TEXT,owner INTEGER);
CREATE TABLE IF NOT EXISTS membership(node INTEGER,version INTEGER,begin INTEGER,end INTEGER,PRIMARY KEY(node,begin)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS membership_active ON membership(end,node);
CREATE TABLE IF NOT EXISTS changes(kind TEXT,snapshot TEXT,provider TEXT,PRIMARY KEY(kind,snapshot,provider)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS locations(snapshot TEXT,provider TEXT,data TEXT,PRIMARY KEY(snapshot,provider));
CREATE TABLE IF NOT EXISTS inventory(snapshot TEXT,path TEXT,parent TEXT,name TEXT,kind TEXT,size INTEGER,mode INTEGER,mtime INTEGER,uid INTEGER,gid INTEGER,link TEXT,hash BLOB,archive TEXT,member TEXT,source TEXT,identity TEXT,source_identity TEXT,pax TEXT,PRIMARY KEY(snapshot,path)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS inventory_identity ON inventory(snapshot,identity,path);
`
