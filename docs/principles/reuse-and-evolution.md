# Reuse and Evolution

## Build-vs-reuse order

Before custom construction:

1. inspect native/framework capability;
2. inspect existing maintained internal capability;
3. inspect suitable external/open-source/commercial capability;
4. configure/extend/adapt when sufficient;
5. build custom behavior when control, differentiation, security, economics, or integration warrants ownership.

## Promotion and extraction rule

The first implementation may be product-local.

A clean local Module/Application boundary is worthwhile when future reuse is credible and cheap, but no package/repository/service should be created merely to signal that possibility.

A second real consumer triggers comparison:

- Are semantics actually the same?
- Are lifecycle/compatibility requirements the same?
- Would shared ownership reduce total maintenance?
- Would extraction create coupling between otherwise independent products?

Promote into Foundation ownership only if the answer is materially favorable.

A third converged consumer is stronger evidence, not a mandatory threshold.

Promotion into the Foundation and extraction into an independently versioned package are separate decisions. A reusable Foundation Module may remain in the same repository/package indefinitely when independent distribution adds no value.

Shared Foundation Core/runtime should use its supported versioned consumption path rather than permanent unmanaged source copies in each product.

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
