// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/lyft/ratelimit/src/config (interfaces: RateLimitConfig,RateLimitConfigLoader)

// Package mock_config is a generated GoMock package.
package mock_config

import (
	context "context"
	gomock "github.com/golang/mock/gomock"
	gostats "github.com/lyft/gostats"
	ratelimit "github.com/lyft/ratelimit/proto/ratelimit"
	config "github.com/lyft/ratelimit/src/config"
	reflect "reflect"
)

// MockRateLimitConfig is a mock of RateLimitConfig interface
type MockRateLimitConfig struct {
	ctrl     *gomock.Controller
	recorder *MockRateLimitConfigMockRecorder
}

// MockRateLimitConfigMockRecorder is the mock recorder for MockRateLimitConfig
type MockRateLimitConfigMockRecorder struct {
	mock *MockRateLimitConfig
}

// NewMockRateLimitConfig creates a new mock instance
func NewMockRateLimitConfig(ctrl *gomock.Controller) *MockRateLimitConfig {
	mock := &MockRateLimitConfig{ctrl: ctrl}
	mock.recorder = &MockRateLimitConfigMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockRateLimitConfig) EXPECT() *MockRateLimitConfigMockRecorder {
	return m.recorder
}

// Dump mocks base method
func (m *MockRateLimitConfig) Dump() string {
	ret := m.ctrl.Call(m, "Dump")
	ret0, _ := ret[0].(string)
	return ret0
}

// Dump indicates an expected call of Dump
func (mr *MockRateLimitConfigMockRecorder) Dump() *gomock.Call {
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Dump", reflect.TypeOf((*MockRateLimitConfig)(nil).Dump))
}

// GetLimit mocks base method
func (m *MockRateLimitConfig) GetLimit(arg0 context.Context, arg1 string, arg2 *ratelimit.RateLimitDescriptor) *config.RateLimit {
	ret := m.ctrl.Call(m, "GetLimit", arg0, arg1, arg2)
	ret0, _ := ret[0].(*config.RateLimit)
	return ret0
}

// GetLimit indicates an expected call of GetLimit
func (mr *MockRateLimitConfigMockRecorder) GetLimit(arg0, arg1, arg2 interface{}) *gomock.Call {
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetLimit", reflect.TypeOf((*MockRateLimitConfig)(nil).GetLimit), arg0, arg1, arg2)
}

// MockRateLimitConfigLoader is a mock of RateLimitConfigLoader interface
type MockRateLimitConfigLoader struct {
	ctrl     *gomock.Controller
	recorder *MockRateLimitConfigLoaderMockRecorder
}

// MockRateLimitConfigLoaderMockRecorder is the mock recorder for MockRateLimitConfigLoader
type MockRateLimitConfigLoaderMockRecorder struct {
	mock *MockRateLimitConfigLoader
}

// NewMockRateLimitConfigLoader creates a new mock instance
func NewMockRateLimitConfigLoader(ctrl *gomock.Controller) *MockRateLimitConfigLoader {
	mock := &MockRateLimitConfigLoader{ctrl: ctrl}
	mock.recorder = &MockRateLimitConfigLoaderMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockRateLimitConfigLoader) EXPECT() *MockRateLimitConfigLoaderMockRecorder {
	return m.recorder
}

// Load mocks base method
func (m *MockRateLimitConfigLoader) Load(arg0 []config.RateLimitConfigToLoad, arg1 gostats.Scope) config.RateLimitConfig {
	ret := m.ctrl.Call(m, "Load", arg0, arg1)
	ret0, _ := ret[0].(config.RateLimitConfig)
	return ret0
}

// Load indicates an expected call of Load
func (mr *MockRateLimitConfigLoaderMockRecorder) Load(arg0, arg1 interface{}) *gomock.Call {
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Load", reflect.TypeOf((*MockRateLimitConfigLoader)(nil).Load), arg0, arg1)
}
