# Offline SYSTEM.DBF Scan Notes

This note records the current reverse-engineering baseline from the sample files
under `oldpro/`.

The goal is not to complete the final extractor yet. The goal is to make the
bootstrap facts repeatable: identify database basics, locate the most important
dictionary objects in `SYSTEM.DBF`, and record how DBF page headers and
`dm.ctl` together supply tablespace/file information.

## Sample Files

| File | Size | Notes |
| --- | ---: | --- |
| `oldpro/SYSTEM.DBF` | 77,594,624 bytes | System dictionary datafile |
| `oldpro/MAIN.DBF` | 134,217,728 bytes | User data tablespace sample |
| `oldpro/TBS_BIN_TEST01.DBF` | 33,554,432 bytes | Additional user tablespace sample |
| `oldpro/dm.ctl` | 6,144 bytes | Control file with database name, tablespace names, and datafile paths |

## Basic Metadata

Observed from `SYSTEM.DBF`:

| Field | Offset | Encoding | Value | Evidence |
| --- | ---: | --- | ---: | --- |
| Extent size | `0x80` | little-endian `u32` | sample-dependent | Verified as pages per extent in multiple initialized databases |
| Page size | `0x84` | little-endian `u32` | `8192` | Bytes `00 20 00 00`; old parser succeeds with 8192-byte pages |
| Page count | `0x8C` | little-endian `u32` | `9472` | `9472 * 8192 = 77594624` |
| Character set flag | `4 * page_size + 0x2D` | `u8` | sample-dependent | `0=GB18030`, `1=UTF-8`, `2=EUC-KR`; matches online `UNICODE()` / `SF_GET_UNICODE_FLAG()` |

Every DBF page also starts with its physical page address:

| Page offset | Length | Meaning |
| --- | --- | --- |
| `+0x00` | `u16` little-endian | Tablespace / group id |
| `+0x02` | `u16` little-endian | File id inside the tablespace |
| `+0x04` | `u32` little-endian | Page number inside the file |

Examples from the GXS sample:

- `SYSTEM.DBF` page `1`: `00 00 00 00 01 00 00 00` -> group `0`,
  file `0`, page `1`.
- `MAIN.DBF` page `2272`: `04 00 00 00 E0 08 00 00` -> group `4`,
  file `0`, page `2272`.

This page header is useful as a fallback to identify local DBF files by
`(group_id, file_id)` when `dm.ctl` paths do not resolve. It does not by itself
store the full tablespace name or datafile path list; `dm.ctl` remains the
richer source for configured names and paths.

Observed from `dm.ctl`:

| Field | Offset | Encoding | Value |
| --- | ---: | --- | --- |
| Control block size | `0x00` | little-endian `u32` | `4096` |
| Database name | `0x04` | NUL-terminated ASCII | `DAMENG` |

Character-set status:

- The current offline detector reads `UNICODE_FLAG` from `SYSTEM.DBF` page 4
  offset `0x2D`.
- Observed values match online checks:
  - `0`: GB18030
  - `1`: UTF-8
  - `2`: EUC-KR
- `dm.ini` is not required for DDL extraction. It may not contain initialization
  parameters such as page size, extent size, or character set.

## Page And Row Scanning Rule

Before parsing `SYSTEM.DBF` dictionary rows, restore the page protection bytes
described in the next section. The current `SYSOBJECTS` bootstrap scanner uses
these observed page rules:

| Item | Offset / Rule |
| --- | --- |
| Page size | From `SYSTEM.DBF + 0x84`, currently `8192` |
| Slot count | `page + 0x24`, little-endian `u16` |
| Slot array start | `page_size - 8 - slot_count * 2` |
| Slot entry | `u16` row offset inside page |

The protection-byte tail length is modeled separately as:

```text
sectors = page_size / 4096
page_tail_reserved_len = (sectors - 1) * 4 + 12
```

For common page sizes:

| Page size | Sectors | Tail reserved length |
| ---: | ---: | ---: |
| `8192` | `2` | `16` bytes |
| `16384` | `4` | `24` bytes |
| `32768` | `8` | `40` bytes |

## Page Protection Bytes

Some Dameng pages use a protection pattern around 4 KiB sector boundaries. The
four bytes immediately before each 4 KiB boundary may be protection bytes rather
than logical row bytes, with the original logical bytes stored in the page tail.
This has been verified for `SYSTEM.DBF` dictionary rows, but it must not be
applied blindly to ordinary user data pages.

Current restoration rule:

```text
for sector = 1 .. page_size / 4096 - 1:
    source = page + page_tail_reserved_len_start + (sector - 1) * 4
    target = page + sector * 4096 - 4
    copy 4 bytes from source to target only when source bytes look more
    plausible than the in-place target bytes

page_tail_reserved_len_start = page_size - ((page_size / 4096 - 1) * 4 + 12)
```

For an 8 KiB page:

```text
tail start:           page + 0x1FF0
restore source:       page + 0x1FF0 .. 0x1FF3
restore target:       page + 0x0FFC .. 0x0FFF
tail bytes remaining: page + 0x1FF4 .. 0x1FFF
```

Why this matters:

- Short strings can cross `0xFFC..0xFFF`.
- Without restoration, identifiers and comments can contain apparent GB18030
  garbage even when the database character set is correct.
- Some pages already contain the logical bytes at the 4 KiB boundary. Blindly
  restoring from the tail can corrupt valid strings, as seen in the 32 KiB
  `gxs` sample where `CURRENT_TIMESTAMP` crossed `0x4FFC..0x4FFF`; the in-place
  bytes were already `52 52 45 4E` (`RREN`) and had to be kept.
- Dictionary slot-array positioning still uses the fixed 8-byte page trailer:
  `slot_array_start = page_size - 8 - slot_count * 2`.
- Ordinary user data pages can store row/slot offset information in the tail.
  data unload therefore does not run this protection-byte restoration on
  `MAIN.DBF`/user tablespace pages unless a page-specific rule is proven.

Verified examples from `oldpro/gr/SYSTEM.DBF`:

| Table | Column | Page | Logical target | Tail source | Restored bytes |
| --- | --- | ---: | ---: | ---: | --- |
| `BPMDB."bpm_inst_esb_queue_succeed"` | `ProcessedAt` | `3086` | `0x0FFC` | `0x1FF0` | `63 65 73 73` (`cess`) |
| `BPMDB."BDS_Demand_Approval"` | `ABOUT_SYSTEM` | `4227` | `0x0FFC` | `0x1FF0` | `53 54 45 4D` (`STEM`) |

Before restoration, those same columns appeared as `Pro...edAt` and
`ABOUT_SY...` in offline DDL output. After restoration they match online
`DESC` output.

The current `SYSOBJECTS` row layout used for object discovery:

| Field | Row-relative offset | Encoding |
| --- | ---: | --- |
| `ID` | `0x07` | little-endian `u32` |
| `SCHID` | `0x0B` | little-endian `u32` |
| `PID` | `0x0F` | little-endian `i32` |
| `INFO1` | `0x1F` | little-endian `u32`; table organization flags for `DBA_TABLES.IOT_TYPE` |
| `INFO3` | `0x27` | little-endian `u64`; temporary-table flags for `DBA_TABLES.TEMPORARY` and `DURATION` |
| `VALID` | `0x3F` | ASCII `Y` / `N` |
| Object name | `0x40` | short string: `0x80 + length`, then bytes |
| Object type | immediately after name | short string |
| Object subtype | immediately after type | short string |

The current `SYSCOLUMNS` row layout from the old script:

| Field | Row-relative offset | Encoding |
| --- | ---: | --- |
| Row length | `0x01` | little-endian `u16` |
| Table/object id | `0x05` | little-endian `u32` |
| Column id | `0x09` | little-endian `u16` |
| Length | `0x0B` | little-endian `u32` |
| Scale / precision helper | `0x0F` | little-endian `i16` |
| Nullable | `0x11` | ASCII `Y` / `N` |
| Column name | `0x16` | short string |
| Data type | immediately after column name | short string |
| Default value | immediately after data type | short string, filtered conservatively |

Verified user-table examples from the current sample:

- `HR_TEST.EMP_INFO.SALARY`: `TYPE$=NUMBER`, `LENGTH$=10`, `SCALE=2` -> `NUMBER(10,2)`
- `SYSDBA.T.PHONE_NUMBER`: `TYPE$=NUMBER`, `LENGTH$=11`, `SCALE=0` -> `NUMBER(11)`
- `SYSDBA.BIN_TEST2_CHILD.create_time`: `TYPE$=datetime`, `LENGTH$=8`, `SCALE=6` -> `datetime(6)`

## Important Object Locations

These offsets are from the current sample and are useful bootstrap anchors. We
will temporarily treat them as fixed official locations, but they still need to
be verified against more DM versions, page sizes, and initialized databases.

Dictionary page slot arrays are located with a fixed 8-byte page trailer:
`slot_array_start = page_size - 8 - slot_count * 2`. This is separate from the
page-protection tail bytes used to restore bytes around 4 KiB sector boundaries.
Using the protection-tail length for slot-array positioning shifts 8 KiB pages
by 8 bytes and can miss active user objects such as `HR_TEST.EMP_INFO`.

| Requested object | Observed owner | Type | Subtype | Object id | Row offset | Page | Page offset | Slot | Name offset | Note |
| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| `SYS.SYSOBJECTS` | `SYS` | `SCHOBJ` | `STAB` | `0` | `0x8AA8B` | `69` | `0xA8B` | `10` | `0x8AACC` | Primary object catalog |
| `SYS.SYSCOLUMNS` | `SYS` | `SCHOBJ` | `STAB` | `2` | `0x8A062` | `69` | `0x62` | `29` | `0x8A0A3` | Primary column catalog |
| `SYS.SYSTEXTS` | `SYS` | `SCHOBJ` | `STAB` | `5` | `0x8E7D5` | `71` | `0x7D5` | `4` | `0x8E816` | SQL text / definition text |
| `SYS.SYSGRANTS` | `SYS` | `SCHOBJ` | `STAB` | `6` | `0x8A6DE` | `69` | `0x6DE` | `16` | `0x8A71F` | Grants catalog |
| `SYS.SYSHPARTTABLEINFO` | `SYS` | `SCHOBJ` | `STAB` | `19` | `0x8A75E` | `69` | `0x75E` | `15` | `0x8A79F` | Partition table metadata |
| `SYS.SYSDISTABLEINFO` | `SYS` | `SCHOBJ` | `STAB` | `38` | `0x8A388` | `69` | `0x388` | `24` | `0x8A3C9` | Distributed table metadata |
| `SYS.SYSPWDCHGS` | `SYS` | `SCHOBJ` | `STAB` | `12` | `0x8E117` | `71` | `0x117` | `17` | `0x8E158` | Password change metadata |
| `SYS.SYSCONTEXTINDEXES` | `CTISYS` | `SCHOBJ` | `STAB` | `29` | `0x17E2062` | `3057` | `0x62` | `52` | `0x17E20A3` | In this sample the table owner is `CTISYS`, not `SYS` |
| `SYS.SYSTABLECOMMENTS` | `SYS` | `SCHOBJ` | `STAB` | `35` | `0x8E71B` | `71` | `0x71B` | `8` | `0x8E75C` | Table comments |
| `SYS.SYSCOLUMNCOMMENTS` | `SYS` | `SCHOBJ` | `STAB` | `36` | `0x87CCC` | `67` | `0x1CCC` | `1` | `0x87D0D` | Column comments |
| `SYS.SYSUSERS` | `SYS` | `SCHOBJ` | `VIEW` | `16777241` | `0x903C0` | `72` | `0x3C0` | `44` | `0x90401` | In this sample this is a view, not a base table |
| `SYS.SYSOBJINFOS` | `SYS` | `SCHOBJ` | `STAB` | `27` | `0x8AB0C` | `69` | `0xB0C` | `9` | `0x8AB4D` | Object extra info |
| `SYS.SYSCOLINFOS` | `SYS` | `SCHOBJ` | `STAB` | `39` | `0x878A5` | `67` | `0x18A5` | `2` | `0x878E6` | Column extra info |
| `SYS.SYSUSERINI$` | `SYS` | `SCHOBJ` | `STAB` | `42` | `0x9188F` | `72` | `0x188F` | `46` | `0x918D0` | User initialization metadata |
| `SYS.SYSDEPENDENCIES` | `SYS` | `SCHOBJ` | `STAB` | `40` | `0x8A2CF` | `69` | `0x2CF` | `25` | `0x8A310` | Dependency metadata |

Important caveats:

- Raw byte searches find many stale or secondary occurrences. The table above is
  filtered through the page slot array and the `SYSOBJECTS` row parser.
- `SYSUSERS` and `SYSCONTEXTINDEXES` need special handling because their observed
  owner/type differs from the requested `SYS.<name>` base-table assumption.
- Partition metadata is stored in `SYSHPARTTABLEINFO`. A table is treated as
  partitioned when `SYSHPARTTABLEINFO.BASE_TABLE_ID` matches its `SYSOBJECTS.ID`.
  Partition details come from the rows' `PARTITION_TYPE`, `PARTITION_NAME`, and
  `PART_TABLE_ID` values.
- Partition key columns are decoded from `SYSOBJINFOS.TYPE$ = 'TABPART'`
  `BIN_VALUE`, matching the logic used by the official `*_PART_KEY_COLUMNS`
  views. The exporter now uses this to emit real `PARTITION BY RANGE/LIST/HASH`
  table DDL.
- Partition rows may be packed consecutively inside one page slot range. In the
  oldpro sample, page `240` slot `4` starts at `0x5A` and contains
  `T_ORDER_RANGE` partitions `P202401`, `P202402`, and `P202403`; `PMAX` is at
  page `240` slot `17`.
- Schema ids must be resolved dynamically from valid `SYSOBJECTS` rows whose
  object type is `SCH`. User schema ids are not stable across databases. For
  example, the same numeric id that was `SY` in a test instance was
  `MMIS_INNOVATION` in a production sample.

## dm.ctl Findings

`SYSTEM.DBF` is enough to recover object/column dictionary rows, but the
tablespace-to-file mapping is in `dm.ctl`.

Observed `dm.ctl` control entries:

| ID | Name | Name offset | Paths observed before next control entry |
| ---: | --- | ---: | --- |
| `0` | `SYSTEM` | `0x806` | `0x924:/dmdata/DAMENG/SYSTEM.DBF` |
| `1` | `ROLL` | `0xA42` | `0xB60:/dmdata/DAMENG/ROLL.DBF` |
| `2` | `RLOG` | `0xC7E` | `0xD9C:/dmdata/DAMENG/DAMENG01.log`, `0xEB8:/dmdata/DAMENG/DAMENG02.log` |
| `4` | `MAIN` | `0xFD6` | `0x10F4:/dmdata/DAMENG/MAIN.DBF`, `0x120C:/dmdata/DAMENG/HMAIN` |
| `5` | `TBS_BIN_TEST` | `0x131E` | `0x143C:/dmdata/DAMENG/TBS_BIN_TEST01.DBF` |

For the table-space-like entries, the current parser reads `ts_id` as a
little-endian `u32` located at `name_offset - 6`. Example:

- `TBS_BIN_TEST` starts at `0x131E`
- `0x131E - 6 = 0x1318`
- bytes at `0x1318` are `05 00 00 00`, so ID is `5`

`TEMP` may still need a reserved fallback mapping. The old script used
`3 -> TEMP` even though it is not emitted as a normal string entry in this sample
control file.

## Current Interactive Workflow

The Go CLI exposes database recovery only through the interactive shell:

```text
DMDUL> set system .\oldpro\SYSTEM.DBF;
DMDUL> set data_dir .\oldpro;
DMDUL> bootstrap;
DMDUL> list table SYSDBA;
DMDUL> unload table SYSDBA.BIN_TEST2;
DMDUL> unload database;
```

DDL recovery includes partition definitions from `SYSHPARTTABLEINFO` plus
`SYSOBJINFOS.TABPART`. Data recovery reads table, column, and storage metadata
from `SYSTEM.DBF`; when `dm.ctl` is absent, DBF files are identified from their
`(group_id, file_id)` page headers. The current row locator follows the verified
ordinary data-page layout:

- page `+0x24`: slot count
- page `+0x26`: free/end offset
- page `+0x2C`: live record count
- page `+0x38`: B-tree level; `0` means leaf data page, `1` means root/internal
  page. Data unload skips non-zero levels so root separator rows are not
  exported as table rows.
- page `+0x3A`: table assist index id
- row area starts at page `+0x62`
- data-page row slots are read from the fixed 8-byte trailer:
  `slot_array_start = page_size - 8 - slot_count * 2`
- data row length is the first two row bytes in big-endian order; the third byte
  is the observed live/deleted status byte, with `0x00` meaning live row

The first decoder supports ordinary in-row records with fixed integer values,
DM `NUMBER`/`DECIMAL`, 3-byte `DATE`, 8-byte `DATETIME`/`TIMESTAMP`, short
in-row character values including markers above `0xBF` for longer inline text,
and small inline LOB values. Inline `CLOB`/`TEXT` values reuse the character
decoder, while inline `BLOB`/`IMAGE` values reuse the same length markers and are
rendered as `HEXTORAW('...')` in generated INSERT SQL.
In the `SYSDBA.T_LOB_TEST` sample, both inline `CLOB` and `BLOB` values use this
observed envelope inside the outer variable-length value:

- bytes `0..2`: marker prefix `01 27 04`
- byte `3`: observed LOB sequence/type byte; values increment as `01`, `02`,
  `03`, ...
- bytes `4..8`: zero in this sample
- bytes `9..12`: little-endian payload byte length
- bytes `13..`: payload bytes

Example row 1:

- `CLOB_TEXT` outer marker `D2` means 82 bytes follow; the envelope payload
  length at bytes `9..12` is `0x45` / 69 UTF-8 bytes.
- `BLOB_DATA` outer marker `9B` means 27 bytes follow; the envelope payload
  length is `0x0E` / 14 bytes, rendered as
  `HEXTORAW('48656C6C6F2044614D656E672031')`.

Heap/NOBRANCH rows can also store longer inline varchar values with a two-byte
big-endian length, for example `01 04` followed by 260 UTF-8 bytes in
`HR_TEST.T_LOG_HEAP.LOG_TEXT`.
It also handles the observed nullable-row pattern from
`SYSDBA.DM_DATA_ROW_SCAN`: nullable trailing variable columns can be omitted
from the variable-value stream and represented by the row trailer instead of an
`0x80` marker; in that case the trailing nullable zero-valued fixed columns are
treated as `NULL`.

Chained rows, migrated rows, out-of-row LOB payloads, LOB segment/page traversal,
and full null bitmap semantics are still pending research items.

Verified heap/NOBRANCH example:

- Online `DBA_SEGMENTS` for `HR_TEST.T_LOG_HEAP`: `TABLESPACE_NAME=MAIN`,
  `HEADER_FILE=0`, `HEADER_BLOCK=64`, `BLOCKS=16`, `EXTENTS=1`.
- `MAIN.DBF` page `69` has `assist index id=33555503`, `n_rec=5`, and five live
  slot-array row offsets: `0x62`, `0x188`, `0x2AE`, `0x3D4`, `0x4FA`.
- Each row begins with length bytes `01 26`, so the row length is `0x0126`
  bytes. This is why sequential scanning using only `row + 0x01` as a
  little-endian length misses heap rows.
