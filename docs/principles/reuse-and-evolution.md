# Reuse and Evolution

## Build-vs-reuse order

Before custom construction:

1. inspect native/framework capability;
2. inspect existing maintained internal capability;
3. inspect suitable external/open-source/commercial capability;
4. configure/extend/adapt when sufficient;
5. build custom behavior when control, differentiation, security, economics, or integration warrants ownership.

## Extraction rule

The first implementation may be local.

A second real consumer triggers comparison:

- Are semantics actually the same?
- Are lifecycle/compatibility requirements the same?
- Would shared ownership reduce total maintenance?
- Would extraction create coupling between otherwise independent products?

Extract only if the answer is materially favorable.

A third converged consumer is stronger evidence, not a mandatory threshold.

## No abstraction for abstraction's sake

Do not create:

- `OurMailer`;
- `OurRouter`;
- `OurQueue`;
- `OurDatabase`;
- `OurCache`;

merely to hide the framework.

Create a domain/app port when the boundary itself matters, for example:

- multiple real providers;
- an external trust boundary;
- meaningful testing/isolation;
- likely technology replacement;
- a module ownership boundary.

## Evolutionary architecture

Make likely changes cheap without implementing those changes early.

Examples:

- permit a future second notification channel without implementing it now;
- keep a future database adapter possible without supporting every engine now;
- keep module boundaries extractable without deploying microservices now;
- keep AI access explicit without building an agent orchestration platform now.

## Replace last

Replacement is justified by evidence such as:

- material recurring limitation;
- security/compatibility issue;
- unsustainable maintenance;
- measurable performance/cost bottleneck;
- product constraints that cannot be met through bounded extension.

Familiarity or aesthetic preference alone is insufficient.
