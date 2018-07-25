package migration

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"code.cloudfoundry.org/lager"
	"github.com/concourse/atc/db/encryption"
	"github.com/concourse/atc/db/lock"
	"github.com/concourse/atc/db/migration/migrations"
	"github.com/mattes/migrate/source"

	_ "github.com/lib/pq"
	_ "github.com/mattes/migrate/source/file"
)

//go:generate counterfeiter . Bindata

type Bindata interface {
	AssetNames() []string
	Asset(name string) ([]byte, error)
}

type bindataSource struct{}

func (bs *bindataSource) AssetNames() []string {
	return AssetNames()
}

func (bs *bindataSource) Asset(name string) ([]byte, error) {
	return Asset(name)
}

func NewOpenHelper(driver, name string, lockFactory lock.LockFactory, strategy encryption.Strategy) *OpenHelper {
	return &OpenHelper{
		driver,
		name,
		lockFactory,
		strategy,
	}
}

type OpenHelper struct {
	driver         string
	dataSourceName string
	lockFactory    lock.LockFactory
	strategy       encryption.Strategy
}

func (self *OpenHelper) CurrentVersion() (int, error) {
	db, err := sql.Open(self.driver, self.dataSourceName)
	if err != nil {
		return -1, err
	}

	defer db.Close()

	return NewMigrator(db, self.lockFactory, self.strategy).CurrentVersion()
}

func (self *OpenHelper) SupportedVersion() (int, error) {
	db, err := sql.Open(self.driver, self.dataSourceName)
	if err != nil {
		return -1, err
	}

	defer db.Close()

	return NewMigrator(db, self.lockFactory, self.strategy).SupportedVersion()
}

func (self *OpenHelper) Open() (*sql.DB, error) {
	db, err := sql.Open(self.driver, self.dataSourceName)
	if err != nil {
		return nil, err
	}

	if err := NewMigrator(db, self.lockFactory, self.strategy).Up(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

func (self *OpenHelper) OpenAtVersion(version int) (*sql.DB, error) {
	db, err := sql.Open(self.driver, self.dataSourceName)
	if err != nil {
		return nil, err
	}

	if err := NewMigrator(db, self.lockFactory, self.strategy).Migrate(version); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

func (self *OpenHelper) MigrateToVersion(version int) error {
	db, err := sql.Open(self.driver, self.dataSourceName)
	if err != nil {
		return err
	}

	defer db.Close()

	if err := NewMigrator(db, self.lockFactory, self.strategy).Migrate(version); err != nil {
		return err
	}

	return nil
}

type Migrator interface {
	CurrentVersion() (int, error)
	SupportedVersion() (int, error)
	Migrate(version int) error
	Up() error
}

func NewMigrator(db *sql.DB, lockFactory lock.LockFactory, strategy encryption.Strategy) Migrator {
	return NewMigratorForMigrations(db, lockFactory, strategy, &bindataSource{})
}

func NewMigratorForMigrations(db *sql.DB, lockFactory lock.LockFactory, strategy encryption.Strategy, bindata Bindata) Migrator {
	return &migrator{
		db,
		lockFactory,
		strategy,
		lager.NewLogger("migrations"),
		bindata,
	}
}

type migrator struct {
	db          *sql.DB
	lockFactory lock.LockFactory
	strategy    encryption.Strategy
	logger      lager.Logger
	bindata     Bindata
}

func (self *migrator) SupportedVersion() (int, error) {

	latest := filenames(self.bindata.AssetNames()).Latest()

	m, err := source.Parse(latest)
	if err != nil {
		return -1, err
	}

	return int(m.Version), nil
}

func (self *migrator) CurrentVersion() (int, error) {
	var currentVersion int
	var direction string
	err := self.db.QueryRow("SELECT version, direction FROM schema_migrations ORDER BY tstamp DESC LIMIT 1").Scan(&currentVersion, &direction)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return -1, err
	}
	migrations, err := self.Migrations()
	if err != nil {
		return -1, err
	}
	versions := []int{migrations[0].version}
	for _, m := range migrations {
		if m.version > versions[len(versions)-1] {
			versions = append(versions, m.version)
		}
	}
	for i, version := range versions {
		if currentVersion == version && direction == "down" {
			currentVersion = versions[i-1]
			break
		}
	}
	return currentVersion, nil
}

func (self *migrator) Migrate(toVersion int) error {
	_, err := self.db.Exec("CREATE TABLE IF NOT EXISTS schema_migrations (version integer, tstamp timestamp with time zone, direction varchar)")
	if err != nil {
		return err
	}
	err = self.convertLegacySchemaTableToCurrent()
	if err != nil {
		return err
	}
	currentVersion, err := self.CurrentVersion()
	if err != nil {
		return err
	}
	migrations, err := self.Migrations()
	if err != nil {
		return err
	}

	if currentVersion <= toVersion {
		for _, m := range migrations {
			if currentVersion < m.version && m.version <= toVersion && m.direction == "up" {
				err = m.run()
				if err != nil {
					return err
				}
			}
		}
	} else {
		for i := len(migrations) - 1; i >= 0; i-- {
			if currentVersion >= migrations[i].version && migrations[i].version > toVersion && migrations[i].direction == "down" {
				err = migrations[i].run()
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (self *migrator) migrationExists(schemaVersion int) (bool, error) {
	var migrationCount int
	err := self.db.QueryRow("SELECT COUNT(*) FROM schema_migrations where version=$1", schemaVersion).Scan(&migrationCount)
	if err != nil {
		return false, err
	}
	return migrationCount == 1, nil
}

func schemaVersion(assetName string) (int, error) {
	regex := regexp.MustCompile("(\\d+)")
	match := regex.FindStringSubmatch(assetName)
	return strconv.Atoi(match[1])
}

type migration struct {
	version   int
	run       func() error
	direction string
}

func (self *migrator) Migrations() ([]migration, error) {
	migrationList := []migration{}
	assets := self.bindata.AssetNames()
	for _, assetName := range assets {
		asset, err := self.bindata.Asset(assetName)
		if err != nil {
			return nil, err
		}
		version, err := schemaVersion(assetName)
		if err != nil {
			return nil, err
		}
		var m migration
		var runFunc func() error
		var direction string
		if strings.HasSuffix(assetName, ".go") {
			if strings.HasSuffix(assetName, ".up.go") {
				direction = "up"
			} else if strings.HasSuffix(assetName, ".down.go") {
				direction = "down"
			} else {
				return nil, fmt.Errorf("cannot determine migration direction for file '%s'", assetName)
			}
			runFunc = func() error {
				contents := string(asset)
				re := regexp.MustCompile("Up_[0-9]*")
				name := re.FindString(contents)
				err := migrations.NewMigrations(self.db, self.strategy).Run(name)
				return err
			}
		}
		if strings.HasSuffix(assetName, ".sql") {
			if strings.HasSuffix(assetName, ".up.sql") {
				direction = "up"
			} else if strings.HasSuffix(assetName, ".down.sql") {
				direction = "down"
			} else {
				return nil, fmt.Errorf("cannot determine migration direction for file '%s'", assetName)
			}
			runFunc = func() error {
				_, err := self.db.Exec(string(asset))
				return err
			}
		}
		runFuncWrapper := func() error {
			err := runFunc()
			if err != nil {
				return fmt.Errorf("Migration '%s' failed: %v", assetName, err)
			}
			_, err = self.db.Exec("INSERT INTO schema_migrations (version, tstamp, direction) VALUES ($1, current_timestamp, $2)", version, direction)
			return err
		}
		m = migration{version, runFuncWrapper, direction}
		migrationList = append(migrationList, m)
	}
	sort.Slice(migrationList, func(i, j int) bool { return migrationList[i].version < migrationList[j].version })
	return migrationList, nil
}

func (self *migrator) Up() error {
	migrations, err := self.Migrations()
	if err != nil {
		return err
	}
	return self.Migrate(migrations[len(migrations)-1].version)
}

// func (self *migrator) open() (*migrate.Migrate, error) {

// 	forceVersion, err := self.checkLegacyVersion()
// 	if err != nil {
// 		return nil, err
// 	}

// 	s, err := bindata.WithInstance(bindata.Resource(
// 		self.source.AssetNames(),
// 		func(name string) ([]byte, error) {
// 			return Asset(name)
// 		}),
// 	)

// 	d, err := postgres.WithInstance(self.db, &postgres.Config{})
// 	if err != nil {
// 		return nil, err
// 	}

// 	driver := NewDriver(d, self.db, self.strategy)

// 	m, err := migrate.NewWithInstance("go-bindata", s, "postgres", driver)
// 	if err != nil {
// 		return nil, err
// 	}

// 	if forceVersion > 0 {
// 		if err = m.Force(forceVersion); err != nil {
// 			return nil, err
// 		}
// 	}

// 	return m, nil
// }

// func (self *migrator) openWithLock() (*migrate.Migrate, lock.Lock, error) {

// 	var err error
// 	var acquired bool
// 	var newLock lock.Lock

// 	if self.lockFactory != nil {
// 		for {
// 			newLock, acquired, err = self.lockFactory.Acquire(self.logger, lock.NewDatabaseMigrationLockID())

// 			if err != nil {
// 				return nil, nil, err
// 			}

// 			if acquired {
// 				break
// 			}

// 			time.Sleep(1 * time.Second)
// 		}
// 	}

// 	m, err := self.open()

// 	if err != nil && newLock != nil {
// 		newLock.Release()
// 		return nil, nil, err
// 	}

// 	return m, newLock, err
// }

func (self *migrator) existLegacyVersion() bool {
	var exists bool
	err := self.db.QueryRow("SELECT EXISTS ( SELECT 1 FROM information_schema.tables WHERE table_name = 'migration_version')").Scan(&exists)
	return err != nil || exists
}

func (self *migrator) convertLegacySchemaTableToCurrent() error {
	oldMigrationLastVersion := 189
	newMigrationStartVersion := 1510262030

	var err error
	var dbVersion int

	exists := self.existLegacyVersion()
	if !exists {
		return nil
	}

	if err = self.db.QueryRow("SELECT version FROM migration_version").Scan(&dbVersion); err != nil {
		return err
	}

	if dbVersion != oldMigrationLastVersion {
		return fmt.Errorf("Must upgrade from db version %d (concourse 3.6.0), current db version: %d", oldMigrationLastVersion, dbVersion)
	}

	if _, err = self.db.Exec("DROP TABLE IF EXISTS migration_version"); err != nil {
		return err
	}

	_, err = self.db.Exec("INSERT INTO schema_migrations (version) VALUES ($1)", newMigrationStartVersion)
	if err != nil {
		return err
	}

	return nil
}

type filenames []string

func (m filenames) Len() int {
	return len(m)
}

func (m filenames) Swap(i, j int) {
	m[i], m[j] = m[j], m[i]
}

func (m filenames) Less(i, j int) bool {
	m1, _ := source.Parse(m[i])
	m2, _ := source.Parse(m[j])
	return m1.Version < m2.Version
}

func (m filenames) Latest() string {
	matches := []string{}

	for _, match := range m {
		if _, err := source.Parse(match); err == nil {
			matches = append(matches, match)
		}
	}

	sort.Sort(filenames(matches))

	return matches[len(matches)-1]
}
