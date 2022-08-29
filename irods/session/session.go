package session

import (
	"fmt"
	"sync"

	"github.com/cyverse/go-irodsclient/irods/connection"
	"github.com/cyverse/go-irodsclient/irods/metrics"
	"github.com/cyverse/go-irodsclient/irods/types"
	log "github.com/sirupsen/logrus"
)

// IRODSSession manages connections to iRODS
type IRODSSession struct {
	account              *types.IRODSAccount
	config               *IRODSSessionConfig
	connectionPool       *ConnectionPool
	sharedConnections    map[*connection.IRODSConnection]int
	startNewTransaction  bool
	poormansRollbackFail bool
	metrics              metrics.IRODSMetrics
	mutex                sync.Mutex
}

// NewIRODSSession create a IRODSSession
func NewIRODSSession(account *types.IRODSAccount, config *IRODSSessionConfig) (*IRODSSession, error) {
	logger := log.WithFields(log.Fields{
		"package":  "session",
		"function": "NewIRODSSession",
	})

	sess := IRODSSession{
		account:           account,
		config:            config,
		sharedConnections: map[*connection.IRODSConnection]int{},

		// transaction
		startNewTransaction:  config.StartNewTransaction,
		poormansRollbackFail: false,

		metrics: metrics.IRODSMetrics{},

		mutex: sync.Mutex{},
	}

	poolConfig := ConnectionPoolConfig{
		Account:          account,
		ApplicationName:  config.ApplicationName,
		InitialCap:       config.ConnectionInitNumber,
		MaxIdle:          config.ConnectionMaxIdle,
		MaxCap:           config.ConnectionMax,
		Lifespan:         config.ConnectionLifespan,
		IdleTimeout:      config.ConnectionIdleTimeout,
		OperationTimeout: config.OperationTimeout,
	}

	pool, err := NewConnectionPool(&poolConfig, &sess.metrics)
	if err != nil {
		logger.WithError(err).Error("cannot create a new connection pool")
		return nil, err
	}
	sess.connectionPool = pool

	// when the user is anonymous, we cannot use transaction since we don't have access to home dir
	if sess.account.ClientUser == "anonymous" {
		sess.startNewTransaction = false
		sess.poormansRollbackFail = true
	}

	// test if it can create a new transaction
	if sess.startNewTransaction {
		logger.Debugf("testing perform poor man rollback")

		conn, _, err := pool.Get()
		if err != nil {
			logger.WithError(err).Error("failed to get a test connection")
			pool.Release()
			return nil, err
		}

		conn.Lock()
		err = conn.PoorMansRollback()
		conn.Unlock()
		if err != nil {
			logger.WithError(err).Warn("could not perform poor man rollback for the connection, disabling poor mans rollback")
			pool.Discard(conn)
			sess.poormansRollbackFail = true
		} else {
			logger.Debugf("using poor man rollback for the connection")
			pool.Return(conn)
		}
	}

	return &sess, nil
}

// GetConfig returns a configuration
func (sess *IRODSSession) GetConfig() *IRODSSessionConfig {
	return sess.config
}

// GetAccount returns an account
func (sess *IRODSSession) GetAccount() *types.IRODSAccount {
	return sess.account
}

// getConnectionFromPool returns an idle connection from pool
func (sess *IRODSSession) getConnectionFromPool() (*connection.IRODSConnection, error) {
	logger := log.WithFields(log.Fields{
		"package":  "session",
		"struct":   "IRODSSession",
		"function": "getConnectionFromPool",
	})

	// get a connection from pool
	conn, isNewConn, err := sess.connectionPool.Get()
	if err != nil {
		logger.WithError(err).Error("failed to get a connection from the pool")
		return nil, err
	}

	if sess.startNewTransaction && !isNewConn {
		// Each irods connection automatically starts a database transaction at initial setup.
		// All queries against irods using a connection will give results corresponding to the time
		// the connection was made, or since the last change using the very same connection.
		// I.e. if connections 1 and 2 are created at the same time, and connection 1 does an update,
		// connection 2 will not see it until any other change is made using connection 2.
		// The connection we get here from the connection pool might be old, and we might miss
		// changes that happened in parallel connections. We fix this by doing a rollback operation,
		// which will do nothing to the database (there are no operations staged for commit/rollback),
		// but which will close the current transaction and starts a new one - refreshing the view for
		// future queries.
		if sess.poormansRollbackFail {
			// always use new connection
			sess.connectionPool.Discard(conn)

			conn, err = sess.connectionPool.GetNew()
			if err != nil {
				logger.WithError(err).Error("failed to create a new connection")
				return nil, err
			}
		} else {
			conn.Lock()
			err = conn.PoorMansRollback()
			conn.Unlock()
			if err != nil {
				logger.WithError(err).Warn("could not perform poor man rollback for the connection, creating a new connection")
				sess.connectionPool.Discard(conn)
				sess.poormansRollbackFail = true

				conn, err = sess.connectionPool.GetNew()
				if err != nil {
					logger.WithError(err).Error("failed to create a new connection")
					return nil, err
				}
			}
		}
	}

	return conn, nil
}

// AcquireConnection returns an idle connection
func (sess *IRODSSession) AcquireConnection() (*connection.IRODSConnection, error) {
	logger := log.WithFields(log.Fields{
		"package":  "session",
		"struct":   "IRODSSession",
		"function": "AcquireConnection",
	})

	sess.mutex.Lock()
	defer sess.mutex.Unlock()

	// check if there are available connections in the pool
	if sess.connectionPool.AvailableConnections() > 0 {
		// try to get it from the pool
		conn, err := sess.getConnectionFromPool()
		// ignore error this happens when connections in the pool are all occupied
		if err != nil {
			if err != ErrConnectionPoolFull {
				logger.WithError(err).Error("failed to get a connection from the pool")
				// fall below
			}
		} else {
			// put to share
			if shares, ok := sess.sharedConnections[conn]; ok {
				shares++
				sess.sharedConnections[conn] = shares
			} else {
				sess.sharedConnections[conn] = 1
			}

			return conn, nil
		}
	}

	// failed to get connection from pool
	// find a connection from shared connection list that has minimum share count
	logger.Debug("Share an in-use connection as it cannot create a new connection")
	minShare := 0
	var minShareConn *connection.IRODSConnection
	for sharedConn, shareCount := range sess.sharedConnections {
		if minShare == 0 || shareCount < minShare {
			minShare = shareCount
			minShareConn = sharedConn
		}

		if minShare == 1 {
			// can't be smaller
			break
		}
	}

	if minShareConn == nil {
		sess.metrics.IncreaseCounterForConnectionPoolFailures(1)
		return nil, fmt.Errorf("failed to get a shared connection")
	}

	// update
	minShare++
	sess.sharedConnections[minShareConn] = minShare

	return minShareConn, nil
}

// AcquireConnectionsMulti returns idle connections
func (sess *IRODSSession) AcquireConnectionsMulti(number int) ([]*connection.IRODSConnection, error) {
	logger := log.WithFields(log.Fields{
		"package":  "session",
		"struct":   "IRODSSession",
		"function": "AcquireConnectionsMulti",
	})

	sess.mutex.Lock()
	defer sess.mutex.Unlock()

	connections := map[*connection.IRODSConnection]bool{}

	// check if there are available connections in the pool
	for i := 0; i < number; i++ {
		if sess.connectionPool.AvailableConnections() > 0 {
			// try to get it from the pool
			conn, err := sess.getConnectionFromPool()
			if err != nil {
				if err != ErrConnectionPoolFull {
					logger.WithError(err).Error("failed to get a connection from the pool")
				}

				// fall through
				break
			} else {
				connections[conn] = true

				// put to share
				if shares, ok := sess.sharedConnections[conn]; ok {
					shares++
					sess.sharedConnections[conn] = shares
				} else {
					sess.sharedConnections[conn] = 1
				}
			}
		} else {
			break
		}
	}

	connectionsInNeed := number - len(connections)

	// failed to get connection from pool
	// find a connection from shared connection
	logger.Debug("Share an in-use connection as it cannot create a new connection")
	for connectionsInNeed > 0 {
		for sharedConn, shareCount := range sess.sharedConnections {
			shareCount++

			connections[sharedConn] = true
			sess.sharedConnections[sharedConn] = shareCount

			connectionsInNeed--
			if connectionsInNeed <= 0 {
				break
			}
		}
	}

	acquiredConnections := []*connection.IRODSConnection{}
	for conn := range connections {
		acquiredConnections = append(acquiredConnections, conn)
	}

	return acquiredConnections, nil
}

// ReturnConnection returns an idle connection
func (sess *IRODSSession) ReturnConnection(conn *connection.IRODSConnection) error {
	logger := log.WithFields(log.Fields{
		"package":  "session",
		"struct":   "IRODSSession",
		"function": "ReturnConnection",
	})

	sess.mutex.Lock()
	defer sess.mutex.Unlock()

	if share, ok := sess.sharedConnections[conn]; ok {
		share--
		if share <= 0 {
			// no share
			delete(sess.sharedConnections, conn)

			if sess.startNewTransaction && sess.poormansRollbackFail {
				// discard, since we cannot reuse the connection
				sess.connectionPool.Discard(conn)
				return nil
			}

			err := sess.connectionPool.Return(conn)
			if err != nil {
				logger.WithError(err).Error("failed to return an idle connection")
				return err
			}
		} else {
			sess.sharedConnections[conn] = share
		}
	}

	return nil
}

// DiscardConnection discards a connection
func (sess *IRODSSession) DiscardConnection(conn *connection.IRODSConnection) error {
	sess.mutex.Lock()
	defer sess.mutex.Unlock()

	if share, ok := sess.sharedConnections[conn]; ok {
		share--
		if share <= 0 {
			// no share
			delete(sess.sharedConnections, conn)

			sess.connectionPool.Discard(conn)
			return nil
		} else {
			sess.sharedConnections[conn] = share
		}
	}

	return nil
}

// Release releases all connections
func (sess *IRODSSession) Release() {
	sess.mutex.Lock()
	defer sess.mutex.Unlock()

	// we don't disconnect connections here,
	// we will disconnect it when calling pool.Release
	sess.sharedConnections = map[*connection.IRODSConnection]int{}

	sess.connectionPool.Release()
}

// Connections returns the number of connections in the pool
func (sess *IRODSSession) ConnectionTotal() int {
	sess.mutex.Lock()
	defer sess.mutex.Unlock()

	return sess.connectionPool.OpenConnections()
}

// GetMetrics returns metrics
func (sess *IRODSSession) GetMetrics() *metrics.IRODSMetrics {
	return &sess.metrics
}
