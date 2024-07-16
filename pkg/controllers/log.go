package controllers

import (
	"fmt"

	pulsarlog "github.com/apache/pulsar-client-go/pulsar/log"
	"github.com/go-logr/logr"
)

type pulsarLog struct {
	logger logr.Logger
}

func (l pulsarLog) SubLogger(fields pulsarlog.Fields) pulsarlog.Logger {
	newlogger := l.logger
	for name, value := range fields {
		newlogger = newlogger.WithValues(name, value)
	}
	return pulsarLog{logger: newlogger}
}

func (l pulsarLog) WithFields(fields pulsarlog.Fields) pulsarlog.Entry {
	return l.SubLogger(fields)
}

func (l pulsarLog) WithField(name string, value interface{}) pulsarlog.Entry {
	return pulsarLog{logger: l.logger.WithValues(name, value)}
}

func (l pulsarLog) WithError(err error) pulsarlog.Entry {
	return pulsarLog{logger: l.logger.WithValues("err", err)}
}

func (l pulsarLog) Debug(args ...interface{}) {
	l.logger.V(2).Info(fmt.Sprint(args...))
}

func (l pulsarLog) Info(args ...interface{}) {
	l.logger.Info(fmt.Sprint(args...))
}

func (l pulsarLog) Warn(args ...interface{}) {
	l.logger.Info(fmt.Sprint(args...))
}

func (l pulsarLog) Error(args ...interface{}) {
	l.logger.Info(fmt.Sprint(args...))
}

func (l pulsarLog) Debugf(format string, args ...interface{}) {
	l.logger.V(2).Info(fmt.Sprintf(format, args...))
}

func (l pulsarLog) Infof(format string, args ...interface{}) {
	l.logger.Info(fmt.Sprintf(format, args...))
}

func (l pulsarLog) Warnf(format string, args ...interface{}) {
	l.logger.Info(fmt.Sprintf(format, args...))
}

func (l pulsarLog) Errorf(format string, args ...interface{}) {
	l.logger.Info(fmt.Sprintf(format, args...))
}

func newPulsarLog(logger Logger) pulsarLog {
	return pulsarLog{logger: logger.WithName("pulsar")}
}
