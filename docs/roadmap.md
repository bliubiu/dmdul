# dmdul Roadmap

## Goal

Build an offline Dameng database extraction CLI that can recover table DDL and
table data from database files when the database instance cannot start normally.

## Suggested Milestones

1. File inspection
   - Read large database files safely.
   - Print file metadata and page samples.
   - Record known page sizes and file header patterns.

2. Page parser
   - Identify page size.
   - Decode common page headers.
   - Classify data pages, dictionary pages, and unsupported pages.

3. Dictionary recovery
   - Locate system catalog objects.
   - Recover table, column, type, and storage metadata.
   - Generate initial `CREATE TABLE` statements.

4. Row extraction
   - Decode row headers and column values.
   - Support NULL flags, variable-length fields, numeric types, text types, and
     date/time types.
   - Export data as SQL inserts and CSV.

5. Reliability
   - Add corruption-tolerant scanning.
   - Add progress reporting.
   - Add resumable exports.
