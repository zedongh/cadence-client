// Copyright (c) 2017-2020 Uber Technologies Inc.
// Portions of the Software are attributed to Copyright (c) 2020 Temporal Technologies Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package internal

import (
	"context"
	"fmt"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/uber-go/tally"
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/internal/common/auth"
	"go.uber.org/cadence/internal/common/metrics"
	"go.uber.org/zap"
)

const (
	// QueryTypeStackTrace is the build in query type for Client.QueryWorkflow() call. Use this query type to get the call
	// stack of the workflow. The result will be a string encoded in the EncodedValue.
	QueryTypeStackTrace string = "__stack_trace"

	// QueryTypeOpenSessions is the build in query type for Client.QueryWorkflow() call. Use this query type to get all open
	// sessions in the workflow. The result will be a list of SessionInfo encoded in the EncodedValue.
	QueryTypeOpenSessions string = "__open_sessions"
)

type (
	// Client is the client for starting and getting information about a workflow executions as well as
	// completing activities asynchronously.
	Client interface {
		// StartWorkflow starts a workflow execution
		// The user can use this to start using a function or workflow type name.
		// Either by
		//     StartWorkflow(ctx, options, "workflowTypeName", arg1, arg2, arg3)
		//     or
		//     StartWorkflow(ctx, options, workflowExecuteFn, arg1, arg2, arg3)
		// The errors it can return:
		//	- EntityNotExistsError, if domain does not exists
		//	- BadRequestError
		//	- WorkflowExecutionAlreadyStartedError
		//	- InternalServiceError
		// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
		// subjected to change in the future.
		StartWorkflow(ctx context.Context, options StartWorkflowOptions, workflow interface{}, args ...interface{}) (*WorkflowExecution, error)

		// ExecuteWorkflow starts a workflow execution and return a WorkflowRun instance and error
		// The user can use this to start using a function or workflow type name.
		// Either by
		//     ExecuteWorkflow(ctx, options, "workflowTypeName", arg1, arg2, arg3)
		//     or
		//     ExecuteWorkflow(ctx, options, workflowExecuteFn, arg1, arg2, arg3)
		// The errors it can return:
		//	- EntityNotExistsError, if domain does not exists
		//	- BadRequestError
		//	- InternalServiceError
		//
		// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
		// subjected to change in the future.
		//
		// WorkflowRun has three methods:
		//  - GetID() string: which return workflow ID (which is same as StartWorkflowOptions.ID if provided)
		//  - GetRunID() string: which return the first started workflow run ID (please see below)
		//  - Get(ctx context.Context, valuePtr interface{}) error: which will fill the workflow
		//    execution result to valuePtr, if workflow execution is a success, or return corresponding
		//    error. This is a blocking API.
		// NOTE: if the started workflow return ContinueAsNewError during the workflow execution, the
		// return result of GetRunID() will be the started workflow run ID, not the new run ID caused by ContinueAsNewError,
		// however, Get(ctx context.Context, valuePtr interface{}) will return result from the run which did not return ContinueAsNewError.
		// Say ExecuteWorkflow started a workflow, in its first run, has run ID "run ID 1", and returned ContinueAsNewError,
		// the second run has run ID "run ID 2" and return some result other than ContinueAsNewError:
		// GetRunID() will always return "run ID 1" and  Get(ctx context.Context, valuePtr interface{}) will return the result of second run.
		// NOTE: DO NOT USE THIS API INSIDE A WORKFLOW, USE workflow.ExecuteChildWorkflow instead
		ExecuteWorkflow(ctx context.Context, options StartWorkflowOptions, workflow interface{}, args ...interface{}) (WorkflowRun, error)

		// GetWorkfow retrieves a workflow execution and return a WorkflowRun instance
		// - workflow ID of the workflow.
		// - runID can be default(empty string). if empty string then it will pick the last running execution of that workflow ID.
		//
		// WorkflowRun has three methods:
		//  - GetID() string: which return workflow ID (which is same as StartWorkflowOptions.ID if provided)
		//  - GetRunID() string: which return the first started workflow run ID (please see below)
		//  - Get(ctx context.Context, valuePtr interface{}) error: which will fill the workflow
		//    execution result to valuePtr, if workflow execution is a success, or return corresponding
		//    error. This is a blocking API.
		// NOTE: if the retrieved workflow returned ContinueAsNewError during the workflow execution, the
		// return result of GetRunID() will be the retrieved workflow run ID, not the new run ID caused by ContinueAsNewError,
		// however, Get(ctx context.Context, valuePtr interface{}) will return result from the run which did not return ContinueAsNewError.
		GetWorkflow(ctx context.Context, workflowID string, runID string) WorkflowRun

		// SignalWorkflow sends a signals to a workflow in execution
		// - workflow ID of the workflow.
		// - runID can be default(empty string). if empty string then it will pick the running execution of that workflow ID.
		// - signalName name to identify the signal.
		// The errors it can return:
		//	- EntityNotExistsError
		//	- InternalServiceError
		//	- WorkflowExecutionAlreadyCompletedError
		SignalWorkflow(ctx context.Context, workflowID string, runID string, signalName string, arg interface{}) error

		// SignalWithStartWorkflow sends a signal to a running workflow.
		// If the workflow is not running or not found, it starts the workflow and then sends the signal in transaction.
		// - workflowID, signalName, signalArg are same as SignalWorkflow's parameters
		// - options, workflow, workflowArgs are same as StartWorkflow's parameters
		// Note: options.WorkflowIDReusePolicy is default to WorkflowIDReusePolicyAllowDuplicate in this API;
		// while in StartWorkflow/ExecuteWorkflow APIs it is default to WorkflowIdReusePolicyAllowDuplicateFailedOnly.
		// The errors it can return:
		//  - EntityNotExistsError, if domain does not exist
		//  - BadRequestError
		//	- InternalServiceError
		SignalWithStartWorkflow(ctx context.Context, workflowID string, signalName string, signalArg interface{},
			options StartWorkflowOptions, workflow interface{}, workflowArgs ...interface{}) (*WorkflowExecution, error)

		// CancelWorkflow cancels a workflow in execution
		// - workflow ID of the workflow.
		// - runID can be default(empty string). if empty string then it will pick the running execution of that workflow ID.
		// The errors it can return:
		//	- EntityNotExistsError
		//	- BadRequestError
		//	- InternalServiceError
		//	- WorkflowExecutionAlreadyCompletedError
		CancelWorkflow(ctx context.Context, workflowID string, runID string) error

		// TerminateWorkflow terminates a workflow execution.
		// workflowID is required, other parameters are optional.
		// - workflow ID of the workflow.
		// - runID can be default(empty string). if empty string then it will pick the running execution of that workflow ID.
		// The errors it can return:
		//	- EntityNotExistsError
		//	- BadRequestError
		//	- InternalServiceError
		//	- WorkflowExecutionAlreadyCompletedError
		TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string, details []byte) error

		// GetWorkflowHistory gets history events of a particular workflow
		// - workflow ID of the workflow.
		// - runID can be default(empty string). if empty string then it will pick the last running execution of that workflow ID.
		// - whether use long poll for tracking new events: when the workflow is running, there can be new events generated during iteration
		// 	 of HistoryEventIterator, if isLongPoll == true, then iterator will do long poll, tracking new history event, i.e. the iteration
		//   will not be finished until workflow is finished; if isLongPoll == false, then iterator will only return current history events.
		// - whether return all history events or just the last event, which contains the workflow execution end result
		// Example:-
		//	To iterate all events,
		//		iter := GetWorkflowHistory(ctx, workflowID, runID, isLongPoll, filterType)
		//		events := []*shared.HistoryEvent{}
		//		for iter.HasNext() {
		//			event, err := iter.Next()
		//			if err != nil {
		//				return err
		//			}
		//			events = append(events, event)
		//		}
		GetWorkflowHistory(ctx context.Context, workflowID string, runID string, isLongPoll bool, filterType s.HistoryEventFilterType) HistoryEventIterator

		// CompleteActivity reports activity completed.
		// activity Execute method can return acitivity.activity.ErrResultPending to
		// indicate the activity is not completed when it's Execute method returns. In that case, this CompleteActivity() method
		// should be called when that activity is completed with the actual result and error. If err is nil, activity task
		// completed event will be reported; if err is CanceledError, activity task cancelled event will be reported; otherwise,
		// activity task failed event will be reported.
		// An activity implementation should use GetActivityInfo(ctx).TaskToken function to get task token to use for completion.
		// Example:-
		//	To complete with a result.
		//  	CompleteActivity(token, "Done", nil)
		//	To fail the activity with an error.
		//      CompleteActivity(token, nil, cadence.NewCustomError("reason", details)
		// The activity can fail with below errors ErrorWithDetails, TimeoutError, CanceledError.
		CompleteActivity(ctx context.Context, taskToken []byte, result interface{}, err error) error

		// CompleteActivityById reports activity completed.
		// Similar to CompleteActivity, but may save user from keeping taskToken info.
		// activity Execute method can return activity.ErrResultPending to
		// indicate the activity is not completed when it's Execute method returns. In that case, this CompleteActivityById() method
		// should be called when that activity is completed with the actual result and error. If err is nil, activity task
		// completed event will be reported; if err is CanceledError, activity task cancelled event will be reported; otherwise,
		// activity task failed event will be reported.
		// An activity implementation should use activityID provided in ActivityOption to use for completion.
		// domain name, workflowID, activityID are required, runID is optional.
		// The errors it can return:
		//  - ErrorWithDetails
		//  - TimeoutError
		//  - CanceledError
		CompleteActivityByID(ctx context.Context, domain, workflowID, runID, activityID string, result interface{}, err error) error

		// RecordActivityHeartbeat records heartbeat for an activity.
		// details - is the progress you want to record along with heart beat for this activity.
		// The errors it can return:
		//	- EntityNotExistsError
		//	- InternalServiceError
		RecordActivityHeartbeat(ctx context.Context, taskToken []byte, details ...interface{}) error

		// RecordActivityHeartbeatByID records heartbeat for an activity.
		// details - is the progress you want to record along with heart beat for this activity.
		// The errors it can return:
		//	- EntityNotExistsError
		//	- InternalServiceError
		RecordActivityHeartbeatByID(ctx context.Context, domain, workflowID, runID, activityID string, details ...interface{}) error

		// ListClosedWorkflow gets closed workflow executions based on request filters
		// The errors it can return:
		//  - BadRequestError
		//  - InternalServiceError
		//  - EntityNotExistError
		ListClosedWorkflow(ctx context.Context, request *s.ListClosedWorkflowExecutionsRequest) (*s.ListClosedWorkflowExecutionsResponse, error)

		// ListClosedWorkflow gets open workflow executions based on request filters
		// The errors it can return:
		//  - BadRequestError
		//  - InternalServiceError
		//  - EntityNotExistError
		ListOpenWorkflow(ctx context.Context, request *s.ListOpenWorkflowExecutionsRequest) (*s.ListOpenWorkflowExecutionsResponse, error)

		// ListWorkflow gets workflow executions based on query. This API only works with ElasticSearch,
		// and will return BadRequestError when using Cassandra or MySQL. The query is basically the SQL WHERE clause,
		// examples:
		//  - "(WorkflowID = 'wid1' or (WorkflowType = 'type2' and WorkflowID = 'wid2'))".
		//  - "CloseTime between '2019-08-27T15:04:05+00:00' and '2019-08-28T15:04:05+00:00'".
		//  - to list only open workflow use "CloseTime = missing"
		// Retrieved workflow executions are sorted by StartTime in descending order when list open workflow,
		// and sorted by CloseTime in descending order for other queries.
		// The errors it can return:
		//  - BadRequestError
		//  - InternalServiceError
		ListWorkflow(ctx context.Context, request *s.ListWorkflowExecutionsRequest) (*s.ListWorkflowExecutionsResponse, error)

		// ListArchivedWorkflow gets archived workflow executions based on query. This API will return BadRequest if Cadence
		// cluster or target domain is not configured for visibility archival or read is not enabled. The query is basically the SQL WHERE clause.
		// However, different visibility archivers have different limitations on the query. Please check the documentation of the visibility archiver used
		// by your domain to see what kind of queries are accept and whether retrieved workflow executions are ordered or not.
		// The errors it can return:
		//  - BadRequestError
		//  - InternalServiceError
		ListArchivedWorkflow(ctx context.Context, request *s.ListArchivedWorkflowExecutionsRequest) (*s.ListArchivedWorkflowExecutionsResponse, error)

		// ScanWorkflow gets workflow executions based on query. This API only works with ElasticSearch,
		// and will return BadRequestError when using Cassandra or MySQL. The query is basically the SQL WHERE clause
		// (see ListWorkflow for query examples).
		// ScanWorkflow should be used when retrieving large amount of workflows and order is not needed.
		// It will use more ElasticSearch resources than ListWorkflow, but will be several times faster
		// when retrieving millions of workflows.
		// The errors it can return:
		//  - BadRequestError
		//  - InternalServiceError
		ScanWorkflow(ctx context.Context, request *s.ListWorkflowExecutionsRequest) (*s.ListWorkflowExecutionsResponse, error)

		// CountWorkflow gets number of workflow executions based on query. This API only works with ElasticSearch,
		// and will return BadRequestError when using Cassandra or MySQL. The query is basically the SQL WHERE clause
		// (see ListWorkflow for query examples).
		// The errors it can return:
		//  - BadRequestError
		//  - InternalServiceError
		CountWorkflow(ctx context.Context, request *s.CountWorkflowExecutionsRequest) (*s.CountWorkflowExecutionsResponse, error)

		// GetSearchAttributes returns valid search attributes keys and value types.
		// The search attributes can be used in query of List/Scan/Count APIs. Adding new search attributes requires cadence server
		// to update dynamic config ValidSearchAttributes.
		GetSearchAttributes(ctx context.Context) (*s.GetSearchAttributesResponse, error)

		// QueryWorkflow queries a given workflow execution and returns the query result synchronously. Parameter workflowID
		// and queryType are required, other parameters are optional. The workflowID and runID (optional) identify the
		// target workflow execution that this query will be send to. If runID is not specified (empty string), server will
		// use the currently running execution of that workflowID. The queryType specifies the type of query you want to
		// run. By default, cadence supports "__stack_trace" as a standard query type, which will return string value
		// representing the call stack of the target workflow. The target workflow could also setup different query handler
		// to handle custom query types.
		// See comments at workflow.SetQueryHandler(ctx Context, queryType string, handler interface{}) for more details
		// on how to setup query handler within the target workflow.
		// - workflowID is required.
		// - runID can be default(empty string). if empty string then it will pick the running execution of that workflow ID.
		// - queryType is the type of the query.
		// - args... are the optional query parameters.
		// The errors it can return:
		//  - BadRequestError
		//  - InternalServiceError
		//  - EntityNotExistError
		//  - QueryFailError
		QueryWorkflow(ctx context.Context, workflowID string, runID string, queryType string, args ...interface{}) (Value, error)

		// QueryWorkflowWithOptions queries a given workflow execution and returns the query result synchronously.
		// See QueryWorkflowWithOptionsRequest and QueryWorkflowWithOptionsResponse for more information.
		// The errors it can return:
		//  - BadRequestError
		//  - InternalServiceError
		//  - EntityNotExistError
		//  - QueryFailError
		QueryWorkflowWithOptions(ctx context.Context, request *QueryWorkflowWithOptionsRequest) (*QueryWorkflowWithOptionsResponse, error)

		// ResetWorkflow reset a given workflow execution and returns a new execution
		// See QueryWorkflowWithOptionsRequest and QueryWorkflowWithOptionsResponse for more information.
		// The errors it can return:
		//  - BadRequestError
		//  - InternalServiceError
		//  - EntityNotExistError
		ResetWorkflow(ctx context.Context, request *s.ResetWorkflowExecutionRequest) (*s.ResetWorkflowExecutionResponse, error)

		// DescribeWorkflowExecution returns information about the specified workflow execution.
		// The errors it can return:
		//  - BadRequestError
		//  - InternalServiceError
		//  - EntityNotExistError
		DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*s.DescribeWorkflowExecutionResponse, error)

		// DescribeTaskList returns information about the target tasklist, right now this API returns the
		// pollers which polled this tasklist in last few minutes.
		// The errors it can return:
		//  - BadRequestError
		//  - InternalServiceError
		//  - EntityNotExistError
		DescribeTaskList(ctx context.Context, tasklist string, tasklistType s.TaskListType) (*s.DescribeTaskListResponse, error)
	}

	// ClientOptions are optional parameters for Client creation.
	ClientOptions struct {
		MetricsScope       tally.Scope
		Identity           string
		DataConverter      DataConverter
		Tracer             opentracing.Tracer
		ContextPropagators []ContextPropagator
		FeatureFlags       FeatureFlags
		Authorization      auth.AuthorizationProvider
	}

	// StartWorkflowOptions configuration parameters for starting a workflow execution.
	// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
	// subjected to change in the future.
	StartWorkflowOptions struct {
		// ID - The business identifier of the workflow execution.
		// Optional: defaulted to a uuid.
		ID string

		// TaskList - The decisions of the workflow are scheduled on this queue.
		// This is also the default task list on which activities are scheduled. The workflow author can choose
		// to override this using activity options.
		// Mandatory: No default.
		TaskList string

		// ExecutionStartToCloseTimeout - The timeout for duration of workflow execution.
		// The resolution is seconds.
		// Mandatory: No default.
		ExecutionStartToCloseTimeout time.Duration

		// DecisionTaskStartToCloseTimeout - The timeout for processing decision task from the time the worker
		// pulled this task. If a decision task is lost, it is retried after this timeout.
		// The resolution is seconds.
		// Optional: defaulted to 10 secs.
		DecisionTaskStartToCloseTimeout time.Duration

		// WorkflowIDReusePolicy - Whether server allow reuse of workflow ID, can be useful
		// for dedup logic if set to WorkflowIdReusePolicyRejectDuplicate.
		// Optional: defaulted to WorkflowIDReusePolicyAllowDuplicateFailedOnly.
		WorkflowIDReusePolicy WorkflowIDReusePolicy

		// RetryPolicy - Optional retry policy for workflow. If a retry policy is specified, in case of workflow failure
		// server will start new workflow execution if needed based on the retry policy.
		RetryPolicy *RetryPolicy

		// CronSchedule - Optional cron schedule for workflow. If a cron schedule is specified, the workflow will run
		// as a cron based on the schedule. The scheduling will be based on UTC time. Schedule for next run only happen
		// after the current run is completed/failed/timeout. If a RetryPolicy is also supplied, and the workflow failed
		// or timeout, the workflow will be retried based on the retry policy. While the workflow is retrying, it won't
		// schedule its next run. If next schedule is due while workflow is running (or retrying), then it will skip that
		// schedule. Cron workflow will not stop until it is terminated or cancelled (by returning cadence.CanceledError).
		// The cron spec is as following:
		// ┌───────────── minute (0 - 59)
		// │ ┌───────────── hour (0 - 23)
		// │ │ ┌───────────── day of the month (1 - 31)
		// │ │ │ ┌───────────── month (1 - 12)
		// │ │ │ │ ┌───────────── day of the week (0 - 6) (Sunday to Saturday)
		// │ │ │ │ │
		// │ │ │ │ │
		// * * * * *
		CronSchedule string

		// Memo - Optional non-indexed info that will be shown in list workflow.
		Memo map[string]interface{}

		// SearchAttributes - Optional indexed info that can be used in query of List/Scan/Count workflow APIs (only
		// supported when Cadence server is using ElasticSearch). The key and value type must be registered on Cadence server side.
		// Use GetSearchAttributes API to get valid key and corresponding value type.
		SearchAttributes map[string]interface{}

		// DelayStartSeconds - Seconds to delay the workflow start
		// The resolution is seconds.
		// Optional: defaulted to 0 seconds
		DelayStart time.Duration
	}

	// RetryPolicy defines the retry policy.
	// Note that the history of activity with retry policy will be different: the started event will be written down into
	// history only when the activity completes or "finally" timeouts/fails. And the started event only records the last
	// started time. Because of that, to check an activity has started or not, you cannot rely on history events. Instead,
	// you can use CLI to describe the workflow to see the status of the activity:
	//     cadence --do <domain> wf desc -w <wf-id>
	RetryPolicy struct {
		// Backoff interval for the first retry. If coefficient is 1.0 then it is used for all retries.
		// Required, no default value.
		InitialInterval time.Duration

		// Coefficient used to calculate the next retry backoff interval.
		// The next retry interval is previous interval multiplied by this coefficient.
		// Must be 1 or larger. Default is 2.0.
		BackoffCoefficient float64

		// Maximum backoff interval between retries. Exponential backoff leads to interval increase.
		// This value is the cap of the interval. Default is 100x of initial interval.
		MaximumInterval time.Duration

		// Maximum time to retry. Either ExpirationInterval or MaximumAttempts is required.
		// When exceeded the retries stop even if maximum retries is not reached yet.
		ExpirationInterval time.Duration

		// Maximum number of attempts. When exceeded the retries stop even if not expired yet.
		// If not set or set to 0, it means unlimited, and rely on ExpirationInterval to stop.
		// Either MaximumAttempts or ExpirationInterval is required.
		MaximumAttempts int32

		// Non-Retriable errors. This is optional. Cadence server will stop retry if error reason matches this list.
		// Error reason for custom error is specified when your activity/workflow return cadence.NewCustomError(reason).
		// Error reason for panic error is "cadenceInternal:Panic".
		// Error reason for any other error is "cadenceInternal:Generic".
		// Error reason for timeouts is: "cadenceInternal:Timeout TIMEOUT_TYPE". TIMEOUT_TYPE could be START_TO_CLOSE or HEARTBEAT.
		// Note, cancellation is not a failure, so it won't be retried.
		NonRetriableErrorReasons []string
	}

	// DomainClient is the client for managing operations on the domain.
	// CLI, tools, ... can use this layer to manager operations on domain.
	DomainClient interface {
		// Register a domain with cadence server
		// The errors it can throw:
		//	- DomainAlreadyExistsError
		//	- BadRequestError
		//	- InternalServiceError
		Register(ctx context.Context, request *s.RegisterDomainRequest) error

		// Describe a domain. The domain has 3 part of information
		// DomainInfo - Which has Name, Status, Description, Owner Email
		// DomainConfiguration - Configuration like Workflow Execution Retention Period In Days, Whether to emit metrics.
		// ReplicationConfiguration - replication config like clusters and active cluster name
		// The errors it can throw:
		//	- EntityNotExistsError
		//	- BadRequestError
		//	- InternalServiceError
		Describe(ctx context.Context, name string) (*s.DescribeDomainResponse, error)

		// Update a domain.
		// The errors it can throw:
		//	- EntityNotExistsError
		//	- BadRequestError
		//	- InternalServiceError
		Update(ctx context.Context, request *s.UpdateDomainRequest) error
	}

	// WorkflowIDReusePolicy defines workflow ID reuse behavior.
	WorkflowIDReusePolicy int

	// ParentClosePolicy defines the action on children when parent is closed
	ParentClosePolicy int
)

const (
	// ParentClosePolicyTerminate means terminating the child workflow
	ParentClosePolicyTerminate ParentClosePolicy = iota
	// ParentClosePolicyRequestCancel means requesting cancellation on the child workflow
	ParentClosePolicyRequestCancel
	// ParentClosePolicyAbandon means not doing anything on the child workflow
	ParentClosePolicyAbandon
)

const (
	// WorkflowIDReusePolicyAllowDuplicateFailedOnly allow start a workflow execution
	// when workflow not running, and the last execution close state is in
	// [terminated, cancelled, timeouted, failed].
	WorkflowIDReusePolicyAllowDuplicateFailedOnly WorkflowIDReusePolicy = iota

	// WorkflowIDReusePolicyAllowDuplicate allow start a workflow execution using
	// the same workflow ID, when workflow not running.
	WorkflowIDReusePolicyAllowDuplicate

	// WorkflowIDReusePolicyRejectDuplicate do not allow start a workflow execution using the same workflow ID at all
	WorkflowIDReusePolicyRejectDuplicate

	// WorkflowIDReusePolicyTerminateIfRunning terminate current running workflow using the same workflow ID if exist,
	// then start a new run in one transaction
	WorkflowIDReusePolicyTerminateIfRunning
)

func getFeatureFlags(options *ClientOptions) FeatureFlags {
	if options != nil {
		return FeatureFlags{
			WorkflowExecutionAlreadyCompletedErrorEnabled: options.FeatureFlags.WorkflowExecutionAlreadyCompletedErrorEnabled,
			PollerAutoScalerEnabled:                       options.FeatureFlags.PollerAutoScalerEnabled,
		}
	}
	return FeatureFlags{}
}

// NewClient creates an instance of a workflow client
func NewClient(service workflowserviceclient.Interface, domain string, options *ClientOptions) Client {
	var identity string
	if options == nil || options.Identity == "" {
		identity = getWorkerIdentity("")
	} else {
		identity = options.Identity
	}
	var metricScope tally.Scope
	if options != nil {
		metricScope = options.MetricsScope
	}
	metricScope = tagScope(metricScope, tagDomain, domain, clientImplHeaderName, clientImplHeaderValue)
	var dataConverter DataConverter
	if options != nil && options.DataConverter != nil {
		dataConverter = options.DataConverter
	} else {
		dataConverter = getDefaultDataConverter()
	}
	var contextPropagators []ContextPropagator
	if options != nil {
		contextPropagators = options.ContextPropagators
	}
	var tracer opentracing.Tracer
	if options != nil && options.Tracer != nil {
		tracer = options.Tracer
		contextPropagators = append(contextPropagators, NewTracingContextPropagator(zap.NewNop(), tracer))
	} else {
		tracer = opentracing.NoopTracer{}
	}
	if options != nil && options.Authorization != nil {
		service = auth.NewWorkflowServiceWrapper(service, options.Authorization)
	}
	service = metrics.NewWorkflowServiceWrapper(service, metricScope)
	return &workflowClient{
		workflowService:    service,
		domain:             domain,
		registry:           newRegistry(),
		metricsScope:       metrics.NewTaggedScope(metricScope),
		identity:           identity,
		dataConverter:      dataConverter,
		contextPropagators: contextPropagators,
		tracer:             tracer,
		featureFlags:       getFeatureFlags(options),
	}
}

// NewDomainClient creates an instance of a domain client, to manager lifecycle of domains.
func NewDomainClient(service workflowserviceclient.Interface, options *ClientOptions) DomainClient {
	var identity string
	if options == nil || options.Identity == "" {
		identity = getWorkerIdentity("")
	} else {
		identity = options.Identity
	}
	var metricScope tally.Scope
	if options != nil {
		metricScope = options.MetricsScope
	}
	metricScope = tagScope(metricScope, tagDomain, "domain-client", clientImplHeaderName, clientImplHeaderValue)
	if options != nil && options.Authorization != nil {
		service = auth.NewWorkflowServiceWrapper(service, options.Authorization)
	}
	service = metrics.NewWorkflowServiceWrapper(service, metricScope)
	return &domainClient{
		workflowService: service,
		metricsScope:    metricScope,
		identity:        identity,
		featureFlags:    getFeatureFlags(options),
	}
}

func (p WorkflowIDReusePolicy) toThriftPtr() *s.WorkflowIdReusePolicy {
	var policy s.WorkflowIdReusePolicy
	switch p {
	case WorkflowIDReusePolicyAllowDuplicate:
		policy = s.WorkflowIdReusePolicyAllowDuplicate
	case WorkflowIDReusePolicyAllowDuplicateFailedOnly:
		policy = s.WorkflowIdReusePolicyAllowDuplicateFailedOnly
	case WorkflowIDReusePolicyRejectDuplicate:
		policy = s.WorkflowIdReusePolicyRejectDuplicate
	case WorkflowIDReusePolicyTerminateIfRunning:
		policy = s.WorkflowIdReusePolicyTerminateIfRunning
	default:
		panic(fmt.Sprintf("unknown workflow reuse policy %v", p))
	}
	return &policy
}

func (p ParentClosePolicy) toThriftPtr() *s.ParentClosePolicy {
	var policy s.ParentClosePolicy
	switch p {
	case ParentClosePolicyAbandon:
		policy = s.ParentClosePolicyAbandon
	case ParentClosePolicyRequestCancel:
		policy = s.ParentClosePolicyRequestCancel
	case ParentClosePolicyTerminate:
		policy = s.ParentClosePolicyTerminate
	default:
		panic(fmt.Sprintf("unknown workflow parent close policy %v", p))
	}
	return &policy
}

// NewValue creates a new encoded.Value which can be used to decode binary data returned by Cadence.  For example:
// User had Activity.RecordHeartbeat(ctx, "my-heartbeat") and then got response from calling Client.DescribeWorkflowExecution.
// The response contains binary field PendingActivityInfo.HeartbeatDetails,
// which can be decoded by using:
//   var result string // This need to be same type as the one passed to RecordHeartbeat
//   NewValue(data).Get(&result)
func NewValue(data []byte) Value {
	return newEncodedValue(data, nil)
}

// NewValues creates a new encoded.Values which can be used to decode binary data returned by Cadence. For example:
// User had Activity.RecordHeartbeat(ctx, "my-heartbeat", 123) and then got response from calling Client.DescribeWorkflowExecution.
// The response contains binary field PendingActivityInfo.HeartbeatDetails,
// which can be decoded by using:
//   var result1 string
//   var result2 int // These need to be same type as those arguments passed to RecordHeartbeat
//   NewValues(data).Get(&result1, &result2)
func NewValues(data []byte) Values {
	return newEncodedValues(data, nil)
}
