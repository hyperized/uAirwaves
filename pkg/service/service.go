package service

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Service represents a systemd service
type Service struct {
	name    string
	enable  bool
	start   bool
	stop    bool
	restart bool
	status  bool
	timeout time.Duration
	verbose bool
}

// Option defines a functional option for Service
type Option func(*Service)

// New creates a new service with the given name
func New(name string, options ...Option) *Service {
	s := &Service{
		name:    name,
		timeout: 30 * time.Second,
	}

	for _, option := range options {
		option(s)
	}

	return s
}

// WithEnable enables the service
func WithEnable() Option {
	return func(s *Service) {
		s.enable = true
	}
}

// WithStart starts the service
func WithStart() Option {
	return func(s *Service) {
		s.start = true
	}
}

// WithStop stops the service
func WithStop() Option {
	return func(s *Service) {
		s.stop = true
	}
}

// WithRestart restarts the service
func WithRestart() Option {
	return func(s *Service) {
		s.restart = true
	}
}

// WithStatus checks the service status
func WithStatus() Option {
	return func(s *Service) {
		s.status = true
	}
}

// WithTimeout sets the timeout for service operations
func WithTimeout(timeout time.Duration) Option {
	return func(s *Service) {
		s.timeout = timeout
	}
}

// WithVerbose enables verbose output
func WithVerbose() Option {
	return func(s *Service) {
		s.verbose = true
	}
}

// Execute runs the configured service operations
func (s *Service) Execute(ctx context.Context) error {
	if s.verbose {
		fmt.Printf("Executing service operations for: %s\n", s.name)
	}

	if s.enable {
		if err := s.enableService(ctx); err != nil {
			return fmt.Errorf("failed to enable service %s: %w", s.name, err)
		}
	}

	if s.stop {
		if err := s.stopService(ctx); err != nil {
			return fmt.Errorf("failed to stop service %s: %w", s.name, err)
		}
	}

	if s.start {
		if err := s.startService(ctx); err != nil {
			return fmt.Errorf("failed to start service %s: %w", s.name, err)
		}
	}

	if s.restart {
		if err := s.restartService(ctx); err != nil {
			return fmt.Errorf("failed to restart service %s: %w", s.name, err)
		}
	}

	if s.status {
		if err := s.checkStatus(ctx); err != nil {
			return fmt.Errorf("failed to check status for service %s: %w", s.name, err)
		}
	}

	return nil
}

// enableService enables the systemd service
func (s *Service) enableService(ctx context.Context) error {
	args := []string{"enable", s.name}
	return s.runCommand(ctx, "systemctl", args)
}

// startService starts the systemd service
func (s *Service) startService(ctx context.Context) error {
	args := []string{"start", s.name}
	return s.runCommand(ctx, "systemctl", args)
}

// stopService stops the systemd service
func (s *Service) stopService(ctx context.Context) error {
	args := []string{"stop", s.name}
	return s.runCommand(ctx, "systemctl", args)
}

// restartService restarts the systemd service
func (s *Service) restartService(ctx context.Context) error {
	args := []string{"restart", s.name}
	return s.runCommand(ctx, "systemctl", args)
}

// checkStatus checks the status of the systemd service
func (s *Service) checkStatus(ctx context.Context) error {
	args := []string{"status", s.name}
	return s.runCommand(ctx, "systemctl", args)
}

// runCommand executes a system command with timeout
func (s *Service) runCommand(ctx context.Context, command string, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("command failed: %s %s: %w\nOutput: %s",
			command, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}

	if s.verbose {
		fmt.Printf("Command output: %s\n", strings.TrimSpace(string(output)))
	}

	return nil
}
