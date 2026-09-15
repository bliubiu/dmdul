# HFS Compression Fixtures

Nine small sections from deterministic test tables on DM8
`03134284336-20250117-257733-20132`, x86-64, 8 KiB DBF pages.
These are HFS sections, not DBF pages or complete database files.

- ZIP2 / ZIP9: `COMPRESS LEVEL 2/9 (NI,TX,NB)`.
- SNAP10: `COMPRESS LEVEL 10 (NI,TX,NB)`.
- `_1.bin`: INT NI, NULL every third row, otherwise row number modulo 7.
- `_2.bin`: VARCHAR TX, NULL every fourth row, otherwise `TEXT-<row modulo 11>` plus 100 `x` characters.
- `_3.bin`: BIGINT NB, NULL every fifth row, otherwise row number times 1,000,000,000.

Each fixture is the first 1024-row section of that column. The tests decode all
values, verify NULLs and temporary-file cleanup, and test damaged envelopes.
No passwords or production table data are included. See
[the validation record](../../../../docs/extended-compatibility-20260915.md).
