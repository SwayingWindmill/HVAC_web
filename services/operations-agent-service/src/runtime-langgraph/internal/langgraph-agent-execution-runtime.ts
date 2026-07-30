import {
  Annotation,
  END,
  START,
  StateGraph,
} from '@langchain/langgraph';

import type {
  AgentExecutionRuntime,
  RuntimePlanningResult,
  RuntimeReadPlan,
} from '../../application/index.js';
import {
  LangGraphRuntimeError,
  assertActiveRun,
  copyPlan,
  normalizeProgram,
  positionFor,
  restoreExecution,
  type LangGraphRuntimeProgram,
  type RuntimeExecutionStateV1,
} from './runtime-state.js';

interface RuntimeGraphPlanning {
  readonly plan: RuntimeReadPlan;
  readonly execution: RuntimeExecutionStateV1;
}

const RuntimeGraphState = Annotation.Root({
  execution: Annotation<RuntimeExecutionStateV1>(),
  selectedStepIndex: Annotation<number | null>(),
  planning: Annotation<RuntimeGraphPlanning | null>(),
});

type RuntimeGraphStateValue = typeof RuntimeGraphState.State;

const compileRuntimeGraph = (program: LangGraphRuntimeProgram) => (
  new StateGraph(RuntimeGraphState)
    .addNode('validate_runtime_state', (state: RuntimeGraphStateValue) => ({
      execution: state.execution,
    }))
    .addNode('select_next_step', (state: RuntimeGraphStateValue) => ({
      selectedStepIndex: state.execution.nextStepIndex < program.steps.length
        ? state.execution.nextStepIndex
        : null,
    }))
    .addNode('emit_read_plan', (state: RuntimeGraphStateValue) => {
      const index = state.selectedStepIndex;
      const step = index === null ? undefined : program.steps[index];
      if (index === null || step === undefined) {
        throw new LangGraphRuntimeError(
          'CHECKPOINT_STATE_INVALID',
          'LangGraph selected a READ Step outside the Runtime program.',
        );
      }
      const execution: RuntimeExecutionStateV1 = {
        ...state.execution,
        nextStepIndex: index + 1,
        completedStepIds: [...state.execution.completedStepIds, step.id],
      };
      return {
        execution,
        planning: { plan: copyPlan(step.plan), execution },
      };
    })
    .addNode('finish_program', () => ({ planning: null }))
    .addEdge(START, 'validate_runtime_state')
    .addEdge('validate_runtime_state', 'select_next_step')
    .addConditionalEdges(
      'select_next_step',
      (state: RuntimeGraphStateValue) => (
        state.selectedStepIndex === null ? 'finish_program' : 'emit_read_plan'
      ),
      ['emit_read_plan', 'finish_program'],
    )
    .addEdge('emit_read_plan', END)
    .addEdge('finish_program', END)
    .compile({ name: program.id })
);

export const createLangGraphAgentExecutionRuntime = (
  input: LangGraphRuntimeProgram,
): AgentExecutionRuntime => {
  const program = normalizeProgram(input);
  const graph = compileRuntimeGraph(program);

  const runtime: AgentExecutionRuntime = {
    async planReads(input): Promise<RuntimePlanningResult> {
      const { investigation, runId, checkpoint } = input;
      assertActiveRun(investigation, runId, program);
      const execution = restoreExecution(investigation, runId, checkpoint, program);
      const result = await graph.invoke({
        execution,
        selectedStepIndex: null,
        planning: null,
      }, { recursionLimit: 8 });
      if (result.planning === null) {
        return {
          status: 'UNABLE_TO_CONCLUDE',
          reason: `Runtime program ${program.id} has no remaining READ Step.`,
        };
      }
      return {
        status: 'PLANNED',
        plan: copyPlan(result.planning.plan),
        checkpoint: {
          position: positionFor(result.planning.execution, program),
          opaqueState: JSON.stringify(result.planning.execution),
        },
      };
    },
  };
  return Object.freeze(runtime);
};
