# DM System Dictionary Fields

This note summarizes the Dameng DM8 system dictionary fields that matter for
offline table structure recovery.

Source: <https://eco.dameng.com/document/dm/zh-cn/pm/dm8-admin-manual-appendix1.html>

Important caveats:

- The official appendix describes logical dictionary columns, not the on-disk
  row byte order in `SYSTEM.DBF`.
- `SYSTEM.DBF` dictionary rows may need page-protection byte restoration before
  interpreting row bytes. DM can store the original four bytes before each 4 KiB
  sector boundary in the page tail; otherwise identifiers that cross offsets such
  as `0xFFC..0xFFF` can appear as character-set garbage. Do not apply this
  blindly to ordinary user data pages, whose page tail may hold row/slot offsets.
- In `SYSTEM.DBF`, physical row positions are not stable. Locate rows by page
  size, page slot array, and row parsing, then interpret the logical fields.
- Dameng creates global synonyms for dictionary tables. A name such as
  `SYSOBJECTS` may appear as `TYPE$ = DSYNOM` and point to `SYS.SYSOBJECTS`,
  while the real dictionary table row is usually `TYPE$ = SCHOBJ` and
  `SUBTYPE$ = STAB`.
- If SVI is enabled, dictionary synonyms may point to `V` views instead of the
  base dictionary tables.

## Recovery Priority

For offline DDL recovery, use these tables in roughly this order:

1. `SYSOBJECTS`: object catalog and object ids.
2. `SYSCOLUMNS`: table columns, types, lengths, nullability, defaults.
3. `SYSCOLINFOS`: extra column flags not fully represented in `SYSCOLUMNS`.
4. `SYSINDEXES` and `SYSCONS`: indexes and constraints.
5. `SYSTEXTS`: view, procedure, trigger, package, synonym, user text.
6. `SYSTABLECOMMENTS` and `SYSCOLUMNCOMMENTS`: comments.
7. `SYSHPARTTABLEINFO` and `SYSDISTABLEINFO`: partition/distribution metadata.
8. `SYSDEPENDENCIES`: dependency ordering and references.

## SYSOBJECTS

Purpose: object catalog for users, schemas, dictionary tables, user tables,
views, indexes, constraints, triggers, sequences, synonyms, and related object
types.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `NAME` | `VARCHAR(128)` | Object name. |
| `ID` | `INTEGER` | Object id. This is the join key used by many dictionary tables. |
| `SCHID` | `INTEGER` | Schema id when `TYPE$` is `SCHOBJ` or `TABOBJ`; otherwise usually `0`. |
| `TYPE$` | `VARCHAR(10)` | Main object type. Important values include `UR`, `SCH`, `DSYNOM`, `DIR`, `PROFILE`, `SCHOBJ`, `DMNOBJ`, `TABOBJ`. |
| `SUBTYPE$` | `VARCHAR(10)` | Subtype. Important values include `USER`, `ROLE`, `UTAB`, `STAB`, `VIEW`, `PROC`, `SEQ`, `PKG`, `TRIG`, `DBLINK`, `SYNOM`, `CLASS`, `TYPE`, `JCLASS`, `DOMAIN`, `CLLT`, `CONTEXT`, `PGRP`, `OPERATOR`, `INDEX`, `CNTIND`, `CONS`. |
| `PID` | `INTEGER` | Parent object id. `-1` means not meaningful for that row. |
| `VERSION` | `INTEGER` | Object version. |
| `CRTDATE` | `DATETIME(6)` | Object creation time. |
| `INFO1` | `INTEGER` | Object-kind-specific flags. For tables it includes buffer/fill/branch flags; for constraints it includes column count; for procedures, views, triggers, packages, synonyms, roles, and sequences it stores different flags. |
| `INFO2` | `INTEGER` | Object-kind-specific auxiliary value. For users/tablespaces/databases/tables it can store quota-like page counts; for views it may store base table id. |
| `INFO3` | `BIGINT` | Important for table/user/view/sequence/trigger/constraint metadata. For users it includes default table space ids. For tables it stores several table flags. |
| `INFO4` | `BIGINT` | For tables, low 4 bytes are table dictionary version and high 4 bytes are large-object data version. |
| `INFO5` | `VARBINARY(128)` | Binary auxiliary payload. For tables this can include segment-related data; for users it includes password hash metadata. |
| `INFO6` | `VARBINARY(2048)` | Binary auxiliary payload. For synonyms it stores target schema/object name lengths and bytes; for constraints it can store column id chains. |
| `INFO7` | `BIGINT` | Extra object-kind-specific value. |
| `INFO8` | `VARBINARY(1024)` | Extra payload. For external tables/context/materialized view logs/triggers it stores specialized information. |
| `VALID` | `CHAR(1)` | `Y` means valid, `N` means invalid. |

Offline notes:

- Real dictionary/system table rows are usually `TYPE$ = SCHOBJ`,
  `SUBTYPE$ = STAB`.
- Global synonym rows use `TYPE$ = DSYNOM`, `SCHID = 0`. In the samples, their
  `INFO6`-like on-disk payload contains target owner/name, for example
  `SYSOBJECTS -> SYS.SYSOBJECTS`.
- For user table DDL, start from rows where `TYPE$ = SCHOBJ` and
  `SUBTYPE$ = UTAB`. System tables are `SUBTYPE$ = STAB`.
- For user table organization, `DBA_TABLES.IOT_TYPE` is derived from
  `SYSOBJECTS.INFO1`: when `INFO1 & 0xFFFF0 = 0`, `IOT_TYPE = IOT`. In offline
  DDL this means the table should keep `STORAGE(..., CLUSTERBTR)`; otherwise it
  is a heap-style table and should be recreated with `NOBRANCH` to avoid
  depending on the target instance's `LIST_TABLE` default.
- For global temporary tables, `DBA_TABLES.TEMPORARY` is derived from
  `SYSOBJECTS.INFO3`: `INFO3 & 0x40 != 0` means `TEMPORARY = Y`. When temporary,
  `INFO3 & 0x10000 != 0` maps to `DURATION = SYS$SESSION` / `ON COMMIT
  PRESERVE ROWS`; otherwise it maps to `SYS$TRANSACTION` / `ON COMMIT DELETE
  ROWS`.
- In the current observed `SYSOBJECTS` row format, logical `INFO1` is at
  row-relative offset `0x1F` and logical `INFO3` starts at `0x27`.

## SYSCOLUMNS

Purpose: column definitions for tables, views, and other objects that expose
columns or parameters.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `NAME` | `VARCHAR(128)` | Column name. |
| `ID` | `INTEGER` | Parent object id, joined to `SYSOBJECTS.ID`. |
| `COLID` | `SMALLINT` | Column id/order within the parent object. |
| `TYPE$` | `VARCHAR(128)` | Column data type text. |
| `LENGTH$` | `INTEGER` | Declared length. |
| `SCALE` | `SMALLINT` | Numeric scale, character semantics marker, time precision, or interval precision depending on type. |
| `NULLABLE$` | `CHAR(1)` | Whether NULL is allowed. |
| `DEFVAL` | `VARCHAR(2048)` | Default value text. |
| `INFO1` | `SMALLINT` | Compression/encryption/column-store/view/procedure flags depending on object kind. |
| `INFO2` | `SMALLINT` | Includes autoincrement flag for normal tables; may store original column id for views or group id for column-store tables. |

Offline notes:

- This is the main source for `CREATE TABLE` column lists.
- `TYPE$`, `LENGTH$`, and `SCALE` must be interpreted together.
- `DEFVAL` should be validated before emitting SQL, because stale or binary-like
  bytes can be misread during offline recovery.

## SYSCOLINFOS

Purpose: extra column metadata.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `ID` | `INTEGER` | Table/object id. |
| `COLID` | `SMALLINT` | Column id. |
| `INFO1` | `INTEGER` | Bit flags: virtual column, fixed DEC, `NUMBER_MODE=1` float marker, Oracle-compatible DATE, default `ON NULL`, implicit `NOT NULL`, `ON UPDATE`. |
| `INFO2` | `INTEGER` | First byte records FLOAT precision when `NUMBER_MODE=1`. |
| `INFO3` | `INTEGER` | Reserved. |

Offline notes:

- Join by `(ID, COLID)` to enrich `SYSCOLUMNS`.
- This is needed for high-fidelity DDL around virtual columns, implicit
  `NOT NULL`, `ON NULL`, and `ON UPDATE`.

## SYSINDEXES

Purpose: index definitions.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `ID` | `INTEGER` | Index id, joined to `SYSOBJECTS.ID` or constraint metadata. |
| `ISUNIQUE` | `CHAR(1)` | Whether the index is unique. |
| `GROUPID` | `SMALLINT` | Tablespace id. Map it through `dm.ctl` when possible. |
| `ROOTFILE` | `SMALLINT` | Root file id. `-1` means no B-tree is needed; `-2` means delayed segment allocation. |
| `ROOTPAGE` | `INTEGER` | Root page. `-1` and `-2` have meanings parallel to `ROOTFILE`. |
| `TYPE$` | `CHAR(2)` | Index implementation, such as `BT`, `BM`, `AR`, `IF`, `HS`, `MP`. |
| `XTYPE` | `INTEGER` | Index type bit flags: clustered/secondary/function/global/unique/visible/vector and other markers. |
| `FLAG` | `INTEGER` | Index flags, including system index, virtual index, PK index, temporary-table index, fast pool. |
| `KEYNUM` | `SMALLINT` | Number of key columns. |
| `KEYINFO` | `VARBINARY(816)` | Encoded key column information. |
| `INIT_EXTENTS` | `SMALLINT` | Initial extent count. |
| `BATCH_ALLOC` | `SMALLINT` | Next allocation extent count. |
| `MIN_EXTENTS` | `SMALLINT` | Minimum extent count. |

Offline notes:

- Needed to rebuild `CREATE INDEX` and to connect PK/UNIQUE constraints to their
  backing indexes.
- `GROUPID` is important for `STORAGE(ON ...)` clauses.
- `KEYINFO` needs a separate physical decoder.

## SYSCONS

Purpose: constraints except NOT NULL, NOT VISIBLE, and CLUSTER KEY constraints.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `ID` | `INTEGER` | Constraint id. |
| `TABLEID` | `INTEGER` | Table id. |
| `COLID` | `SMALLINT` | Currently not meaningful in the official description; normally `-1`. |
| `TYPE$` | `CHAR(1)` | `P` primary key, `U` unique, `F` foreign key, `C` check. |
| `VALID` | `CHAR(1)` | Whether the constraint is valid. |
| `INDEXID` | `INTEGER` | Backing index id. |
| `CHECKINFO` | `VARCHAR(2048)` | Check constraint text. |
| `FINDEXID` | `INTEGER` | Referenced index id for foreign keys. |
| `FACTION` | `CHAR(2)` | Foreign-key update/delete actions. |
| `TRIGID` | `INTEGER` | Action trigger id. |

Offline notes:

- Join `TABLEID` to `SYSOBJECTS.ID`.
- Join `INDEXID` / `FINDEXID` to `SYSINDEXES.ID`.
- Constraint names are found through `SYSOBJECTS` rows whose subtype is `CONS`.

## SYSTEXTS

Purpose: text for dictionary objects.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `ID` | `INTEGER` | Owner object id. Applies to views, procedures, sequences, packages, triggers, synonyms, classes, types, domains, collections, contexts, and users. |
| `SEQNO` | `INTEGER` | Text sequence/type marker. For views, `0` is view definition and `1` is query clause. For packages, `0` is spec and `1` is body. For users, `0` and `1` can store long allow/deny address text. |
| `TXT` | `CLOB` | Text content. |

Offline notes:

- Needed for views, triggers, packages, procedures, synonyms, and long user
  address restrictions.
- Inline CLOB text can be decoded through the normal in-row text path. Out-of-row
  CLOB values still need LOB locator and LOB segment/page support.

## SYSGRANTS

Purpose: grants and privileges.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `URID` | `INTEGER` | Granted user/role id. |
| `OBJID` | `INTEGER` | Granted object id; `-1` for database privileges. |
| `COLID` | `INTEGER` | Column id for column privileges; `-1` for non-column privileges. |
| `PRIVID` | `INTEGER` | Privilege id; `-1` when granting a role. |
| `GRANTOR` | `INTEGER` | Grantor id; `-1` when granting a role. |
| `GRANTABLE` | `CHAR(1)` | Whether the privilege can be granted onward. |

Offline notes:

- Role grants are recoverable when `PRIVID = -1`, `COLID = -1`, and `GRANTOR =
  -1`. In the observed physical row layout, the slot points to a 44-byte row:
  row length is at `+0x01`, `URID` at `+0x04`, `OBJID` at `+0x08`, `COLID` at
  `+0x0C`, `PRIVID` at `+0x10`, `GRANTOR` at `+0x14`, and `GRANTABLE` at
  `+0x18`.
- Object privilege grants use the same table, but `PRIVID` to privilege-name
  mapping still needs online `DBA_TAB_PRIVS` samples before enabling generated
  SQL.

## SYSHPARTTABLEINFO

Purpose: partition table metadata.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `BASE_TABLE_ID` | `INTEGER` | Base table id. |
| `PART_TABLE_ID` | `INTEGER` | Partition table id. |
| `PARTITION_TYPE` | `VARCHAR(10)` | Partition type. |
| `PARTITION_NAME` | `VARCHAR(128)` | Partition name. |
| `HIGH_VALUE` | `VARBINARY(8188)` | LIST values or RANGE boundary value; NULL for HASH partitions. |
| `INCLUDE_HIGH_VALUE` | `CHAR(1)` | Whether the boundary/value is included. |
| `RESVD1` | `INTEGER` | For child-table records, first sibling child table id; for template records, subpartition count. |
| `RESVD2` | `INTEGER` | Reserved. |
| `RESVD3` | `INTEGER` | Reserved. |
| `RESVD4` | `VARCHAR(128)` | Reserved. |
| `RESVD5` | `VARCHAR(2000)` | Reserved. |

Offline notes:

- Needed to rebuild partition clauses.
- `HIGH_VALUE` is binary and needs type-aware decoding.
- Offline `DBA_TABLES.PARTITIONED` can be derived from this table: if at
  least one row has `BASE_TABLE_ID = SYSOBJECTS.ID`, the user table is
  partitioned.
- Offline `DBA_TAB_PARTITIONS` / `USER_TAB_PARTITIONS` can be recovered by
  joining `BASE_TABLE_ID` back to `SYSOBJECTS.ID`; `USER_` scope is equivalent
  to applying an owner filter.
- Partition key columns are exposed by the official
  `*_PART_KEY_COLUMNS` views. In observed view SQL they are decoded from
  `SYSOBJINFOS` rows where `TYPE$ = 'TABPART'`: `BIN_VALUE` starts with a
  little-endian key-column count, followed by key `COLID` values every four
  bytes. Join those `COLID` values back to `SYSCOLUMNS`.
- Observed row offsets in `SYSTEM.DBF` records:
  `BASE_TABLE_ID @ +0x05`, `PART_TABLE_ID @ +0x09`,
  `PARTITION_TYPE @ +0x1A`, followed immediately by `PARTITION_NAME`.
- Some active rows are packed consecutively inside one slot range. For example,
  `T_ORDER_RANGE` has `P202401`, `P202402`, and `P202403` in the same slot
  range, while `PMAX` has a separate slot. Partition scanning should walk from a
  slot offset up to the next active slot offset, not only parse one row at the
  slot pointer.

## SYSDISTABLEINFO

Purpose: MPP RANGE/LIST distributed table metadata.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `TABLE_ID` | `INTEGER` | Table id. |
| `SEQNO` | `INTEGER` | Distribution column order. |
| `DIS_TYPE` | `VARCHAR(10)` | Distribution type, such as `LIST` or `RANGE`. |
| `HIGH_VALUE` | `VARBINARY(8188)` | LIST distribution value or RANGE boundary. |
| `INCLUDE_HIGH_VALUE` | `CHAR(1)` | For RANGE, whether the boundary is included; for LIST, always included. |

Offline notes:

- Needed only when recovering MPP distribution clauses.

## SYSPWDCHGS

Purpose: password change history.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `LOGINID` | `INTEGER` | Login id. |
| `OLD_PWD` | `VARCHAR(512)` | Old password hash/text as stored by DM. |
| `NEW_PWD` | `VARCHAR(512)` | New password hash/text as stored by DM. |
| `MODIFIED_TIME` | `DATETIME(6)` | Password modification time. |

Offline notes:

- Not needed for table DDL recovery.
- Treat as sensitive data; do not export by default.

## SYSCONTEXTINDEXES

Purpose: full-text index metadata.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `NAME` | `VARCHAR(128)` | Full-text index name. |
| `ID` | `INTEGER` | Full-text index id. |
| `TABLEID` | `INTEGER` | Base table id. |
| `COLID` | `SMALLINT` | Indexed column id. |
| `UPD_TIMESTAMP` | `DATETIME(6)` | Last related DDL update time. |
| `TIID` | `INTEGER` | `CTI$INDEX_NAME$I` table id. |
| `TDID` | `INTEGER` | `CTI$INDEX_NAME$D` table id. |
| `TPID` | `INTEGER` | `CTI$INDEX_NAME$P` table id. |
| `TNID` | `INTEGER` | `CTI$INDEX_NAME$N` table id. |
| `TRID` | `INTEGER` | `CTI$INDEX_NAME$R` table id. |
| `WSEG_TYPE` | `SMALLINT` | Word segmentation type. |
| `RESVD` | `VARBINARY(100)` | Reserved. |

Offline notes:

- Join `TABLEID` and `COLID` to table/column metadata.
- Auxiliary CTI table ids should be excluded from normal user-table export unless
  full-text index reconstruction is explicitly enabled.

## SYSTABLECOMMENTS

Purpose: table or view comments.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `SCHNAME` | `VARCHAR(128)` | Schema name. |
| `TVNAME` | `VARCHAR(128)` | Table/view name. |
| `TABLE_TYPE` | `VARCHAR(10)` | Object type. |
| `COMMENT$` | `VARCHAR(40000)` | Comment text. |

Offline notes:

- Used to emit `COMMENT ON TABLE`.
- Character set handling matters for non-ASCII comments.

## SYSCOLUMNCOMMENTS

Purpose: column comments.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `SCHNAME` | `VARCHAR(128)` | Schema name. |
| `TVNAME` | `VARCHAR(128)` | Table/view name. |
| `TABLE_TYPE` | `VARCHAR(10)` | Object type. |
| `COMMENT$` | `VARCHAR(4000)` | Comment text. |

Offline notes:

- The official appendix section lists comment metadata, but the displayed field
  set appears shorter than what column comments normally need. Verify with
  physical rows before finalizing the decoder.
- Used to emit `COMMENT ON COLUMN` once column name mapping is confirmed.

## SYSUSERS

Purpose: user metadata and login policy.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `ID` | `INTEGER` | User id. |
| `PASSWORD` | `VARCHAR(512)` | Password data. Sensitive; do not export by default. |
| `AUTHENT_TYPE` | `INTEGER` | Authentication mode. |
| `SESS_PER_USER` | `INTEGER` | Per-user session limits. |
| `CONN_IDLE_TIME` | `INTEGER` | Max idle time. Unit depends on `RESOURCE_FLAG`. |
| `FAILED_NUM` | `INTEGER` | Login failure limit. |
| `LIFE_TIME` | `INTEGER` | Password lifetime in days. |
| `REUSE_TIME` | `INTEGER` | Password reuse interval in days. |
| `REUSE_MAX` | `INTEGER` | Number of password changes before reuse. |
| `LOCK_TIME` | `INTEGER` | Password lock time in minutes. |
| `GRACE_TIME` | `INTEGER` | Password grace period in days. |
| `LOCKED_STATUS` | `SMALLINT` | Manual/unlocked/auto-locked status. |
| `LASTEST_LOCKED` | `DATETIME(0)` | Last lock time. |
| `PWD_POLICY` | `INTEGER` | Password policy. |
| `RN_FLAG` | `INTEGER` | Read-only flag. |
| `ALLOW_ADDR` | `VARCHAR(500)` | Allowed IPs. Longer text moves to `SYSTEXTS`. |
| `NOT_ALLOW_ADDR` | `VARCHAR(500)` | Denied IPs. Longer text moves to `SYSTEXTS`. |
| `ALLOW_DT` | `VARCHAR(500)` | Allowed login time windows. |
| `NOT_ALLOW_DT` | `VARCHAR(500)` | Denied login time windows. |
| `LAST_LOGIN_DTID` | `VARCHAR(128)` | Last login time marker. |
| `LAST_LOGIN_IP` | `VARCHAR(128)` | Last login IP. |
| `FAILED_ATTEMPS` | `INTEGER` | Failed attempts since last successful login. |
| `ENCRYPT_KEY` | `VARCHAR(256)` | Storage encryption key for the user login. |
| `OLD_PASSWORD` | `VARCHAR(512)` | Auxiliary/old password data. Sensitive. |

Offline notes:

- Useful for schema/user reconstruction, but credentials and password history
  should be excluded unless explicitly requested.

## SYSOBJINFOS

Purpose: object dependency/extra information.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `ID` | `INTEGER` | Depended class/object id. |
| `TYPE$` | `VARCHAR(100)` | Dependency or extra-info type. |
| `INT_VALUE` | `INTEGER` | Integer value for that type. |
| `STR_VALUE` | `VARCHAR(2048)` | For domain objects, stores `DOMAIN` plus domain id; otherwise often unused. |
| `BIN_VALUE` | `VARBINARY(2048)` | Reserved/unused in the official description. |

Offline notes:

- Lower priority for ordinary table DDL, but important for domains and special
  object types.

## SYSUSERINI

Purpose: custom user-level INI parameters.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `USER_NAME` | `VARCHAR(128)` | User name. |
| `USER_ID` | `INTEGER` | User id. |
| `PARA_NAME` | `VARCHAR(256)` | INI parameter name. |
| `INT_VALUE` | `BIGINT` | Integer parameter value. |
| `DOUBLE_VALUE` | `DOUBLE` | Floating-point parameter value. |
| `STRING_VALUE` | `VARCHAR(4000)` | String parameter value. |

Offline notes:

- Some `SYSTEM.DBF` samples expose the name as `SYSUSERINI$`; the official
  appendix names the logical dictionary entry `SYSUSERINI`.
- Not needed for basic table DDL, but useful for environment reconstruction.

## SYSDEPENDENCIES

Purpose: dependencies between objects.

| Column | Type | Meaning for recovery |
| --- | --- | --- |
| `ID` | `INTEGER` | Dependent object id. |
| `TYPE$` | `VARCHAR(17)` | Dependent object type, such as table, view, materialized view, index, procedure, function, trigger, sequence, class, type, package, synonym, or domain. |
| `REFED_ID` | `INTEGER` | Referenced object id. |
| `REFED_TYPE$` | `VARCHAR(17)` | Referenced object type. |
| `DEPEND_TYPE` | `VARCHAR(4)` | Usually `HARD`; can be `REF` for materialized view and index dependencies. |

Offline notes:

- Useful for ordering view/procedure/package/trigger creation after base tables.
