package retry

import (
	"time"
	"fmt"
	"testing"
	"github.com/gruntwork-io/terratest/modules/logger"
	"context"
)

type Either struct {
	Result string
	Error  error
}

// Run the specified action and wait up to the specified timeout for it to complete. Return the output of the action if
// it completes on time or fail the test otherwise.
func DoWithTimeout(t *testing.T, actionDescription string, timeout time.Duration, action func() (string, error)) string {
	out, err := DoWithTimeoutE(t, actionDescription, timeout, action)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Run the specified action and wait up to the specified timeout for it to complete. Return the output of the action if
// it completes on time or an error otherwise.
func DoWithTimeoutE(t *testing.T, actionDescription string, timeout time.Duration, action func() (string, error)) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	resultChannel := make(chan Either, 1)

	go func() {
		out, err := action()
		resultChannel <- Either{Result: out, Error: err}
	}()

	select {
	case either := <- resultChannel:
		return either.Result, either.Error
	case <- ctx.Done():
		return "", TimeoutExceeded{Description: actionDescription, Timeout: timeout}
	}
}

// Run the specified action. If it returns a value, return that value. If it returns a FatalError, return that error
// immediately. If it returns any other type of error, sleep for sleepBetweenRetries and try again, up to a maximum of
// maxRetries retries. If maxRetries is exceeded, fail the test.
func DoWithRetry(t *testing.T, actionDescription string, maxRetries int, sleepBetweenRetries time.Duration, action func() (string, error)) string {
	out, err := DoWithRetryE(t, actionDescription, maxRetries, sleepBetweenRetries, action)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Run the specified action. If it returns a value, return that value. If it returns a FatalError, return that error
// immediately. If it returns any other type of error, sleep for sleepBetweenRetries and try again, up to a maximum of
// maxRetries retries. If maxRetries is exceeded, return a MaxRetriesExceeded error.
func DoWithRetryE(t *testing.T, actionDescription string, maxRetries int, sleepBetweenRetries time.Duration, action func() (string, error)) (string, error) {
	for i := 0; i <= maxRetries; i++ {
		logger.Log(t, actionDescription)

		output, err := action()
		if err == nil {
			return output, nil
		}

		if _, isFatalErr := err.(FatalError); isFatalErr {
			logger.Logf(t, "Returning due to fatal error: %v", err)
			return "", err
		}

		logger.Logf(t, "%s returned an error: %s. Sleeping for %s and will try again.", actionDescription, err.Error(), sleepBetweenRetries)
		time.Sleep(sleepBetweenRetries)
	}

	return "", MaxRetriesExceeded{Description: actionDescription, MaxRetries: maxRetries}
}

type Done struct {
	stop chan bool
}

func (done Done) Done() {
	done.stop <- true
}

// Run the specified action in the background (in a goroutine) repeatedly, waiting the specified amount of time between
// repetitions. To stop this action, call the Done() function on the returned value.
func DoInBackgroundUntilStopped(t *testing.T, actionDescription string, sleepBetweenRepeats time.Duration, action func()) Done {
	stop := make(chan bool)

	go func() {
		for {
			logger.Logf(t, "Executing action '%s'", actionDescription)

			action()

			logger.Logf(t, "Sleeping for %s before repeating action '%s'", sleepBetweenRepeats, actionDescription)

			select {
			case <- time.After(sleepBetweenRepeats):
				// Nothing to do, just allow the loop to continue
			case <- stop:
				logger.Logf(t, "Received stop signal for action '%s'.", actionDescription)
				return
			}
		}
	}()

	return Done{stop: stop}
}

// Custom error types

type TimeoutExceeded struct {
	Description string
	Timeout     time.Duration
}

func (err TimeoutExceeded) Error() string {
	return fmt.Sprintf("'%s' did not complete before timeout of %s", err.Description, err.Timeout)
}

type MaxRetriesExceeded struct {
	Description string
	MaxRetries  int
}

func (err MaxRetriesExceeded) Error() string {
	return fmt.Sprintf("'%s' unsuccessful after %d retries", err.Description, err.MaxRetries)
}

// Marker interface for errors that should not be retried
type FatalError struct {
	Underlying error
}

func (err FatalError) Error() string {
	return fmt.Sprintf("FatalError{Underlying: %v}", err.Underlying)
}
