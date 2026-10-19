package store

// AuditFloorDays is the age, in days, below which the database refuses to
// delete an audit row, whatever any code asks (the delete trigger of migration
// 0008, judged by SQLite's own clock). It is the shortest audit retention that
// can be configured; TestAuditFloorMatchesTheMigrationAndTheConfiguration keeps
// the constant, the migration and config.MinAuditRetentionDays equal.
const AuditFloorDays = 30
