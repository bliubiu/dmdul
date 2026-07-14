1. 准备 SYSTEM.DBF、dm.ctl、MAIN.DBF 等文件
2. 启动 dmdul
3. set system
4. set data_dir
5. bootstrap
6. 检查 dmdul_dict
7. 必要时修正 users.tsv/tables.tsv/columns.tsv
8. load dictionary
9. unload database
10. 审核 output/DATABASE_ddl.sql 和 output/DATABASE_data.sql
11. 导入测试库校验
