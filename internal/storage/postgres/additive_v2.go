package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/Ruleshift/server/internal/controlplane"
)

type AdditiveMigrationApplier struct{ platform *Platform }

func (p *Platform) AdditiveMigrationApplier() *AdditiveMigrationApplier {
	return &AdditiveMigrationApplier{platform: p}
}
func (p *Platform) V2ModuleDatabaseName(developerID, moduleID string) (string, error) {
	return moduleDatabaseName(p.cfg.ModuleDatabasePrefix, developerID, moduleID)
}

func (a *AdditiveMigrationApplier) ApplyAdditive(ctx context.Context, version controlplane.Version) error {
	if err := controlplane.ValidateAdditiveMigrations(version.Manifest.DatabaseMigrations); err != nil {
		return err
	}
	name, err := moduleDatabaseName(a.platform.cfg.ModuleDatabasePrefix, version.Ref.DeveloperID, version.Ref.ModuleID)
	if err != nil {
		return err
	}
	if err = ensureDatabase(ctx, a.platform.admin, name); err != nil {
		return err
	}
	url, err := databaseURL(a.platform.controlURL, name)
	if err != nil {
		return err
	}
	db, err := a.platform.openModuleDatabase(ctx, name, url)
	if err != nil {
		return err
	}
	base, err := embeddedMigrations("migrations/module")
	if err != nil {
		return err
	}
	if err = applyMigrations(ctx, db, "ruleshift_module", base); err != nil {
		return err
	}
	migrations := make([]databaseMigration, 0, len(version.Manifest.DatabaseMigrations))
	for _, migration := range version.Manifest.DatabaseMigrations {
		sql, compileErr := compileAdditiveMigration(migration)
		if compileErr != nil {
			return compileErr
		}
		migrations = append(migrations, databaseMigration{Version: migration.Version, Name: migration.Name, SQL: sql})
	}
	return applyMigrations(ctx, db, "external_"+version.Ref.ModuleID, migrations)
}

func compileAdditiveMigration(value controlplane.DatabaseMigration) (string, error) {
	statements := make([]string, 0, len(value.Tables))
	for _, table := range value.Tables {
		columns := make([]string, 0, len(table.Columns)+1)
		primary := []string{}
		for _, column := range table.Columns {
			sqlType, err := externalColumnType(column.Type)
			if err != nil {
				return "", err
			}
			line := "    " + quoteIdentifier(column.Name) + " " + sqlType
			if !column.Nullable || column.PrimaryKey {
				line += " NOT NULL"
			}
			columns = append(columns, line)
			if column.PrimaryKey {
				primary = append(primary, quoteIdentifier(column.Name))
			}
		}
		if len(primary) > 0 {
			columns = append(columns, "    PRIMARY KEY ("+strings.Join(primary, ", ")+")")
		}
		statements = append(statements, "CREATE TABLE "+quoteIdentifier(table.Name)+" (\n"+strings.Join(columns, ",\n")+"\n);")
	}
	return strings.Join(statements, "\n\n"), nil
}
func externalColumnType(value string) (string, error) {
	switch value {
	case "string":
		return "TEXT", nil
	case "int64":
		return "BIGINT", nil
	case "float64":
		return "DOUBLE PRECISION", nil
	case "bool":
		return "BOOLEAN", nil
	case "timestamp":
		return "TIMESTAMPTZ", nil
	case "json":
		return "JSONB", nil
	default:
		return "", fmt.Errorf("unsupported additive column type %q", value)
	}
}
