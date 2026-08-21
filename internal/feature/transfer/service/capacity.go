package transfer_service

import (
	"fmt"
	"math"
)

type CapacityPolicy struct {
	MaxItems        int64
	MaxGroups       int64
	MaxTargets      int64
	MaxItemTargets  int64
	MaxGroupTargets int64
}

type Capacity struct {
	Items        int64
	Groups       int64
	Targets      int64
	ItemTargets  int64
	GroupTargets int64
}

func (policy CapacityPolicy) Validate() error {
	if policy.MaxItems <= 0 ||
		policy.MaxGroups <= 0 ||
		policy.MaxTargets <= 0 ||
		policy.MaxItemTargets <= 0 ||
		policy.MaxGroupTargets <= 0 {
		return fmt.Errorf("transfer capacity limits must be positive")
	}
	return nil
}

func (policy CapacityPolicy) Check(
	items int,
	groups int,
	targets int,
) (Capacity, error) {
	if err := policy.Validate(); err != nil {
		return Capacity{}, err
	}
	if items <= 0 || groups <= 0 || targets <= 0 || groups > items {
		return Capacity{}, fmt.Errorf("invalid transfer capacity inputs")
	}

	capacity := Capacity{
		Items:   int64(items),
		Groups:  int64(groups),
		Targets: int64(targets),
	}
	if capacity.Items > policy.MaxItems ||
		capacity.Groups > policy.MaxGroups ||
		capacity.Targets > policy.MaxTargets {
		return Capacity{}, capacityExceeded(capacity, policy)
	}

	var ok bool
	capacity.ItemTargets, ok = checkedMultiply(capacity.Items, capacity.Targets)
	if !ok {
		return Capacity{}, fmt.Errorf(
			"%w: item-target matrix overflows int64",
			ErrCapacityExceeded,
		)
	}
	capacity.GroupTargets, ok = checkedMultiply(capacity.Groups, capacity.Targets)
	if !ok {
		return Capacity{}, fmt.Errorf(
			"%w: group-target matrix overflows int64",
			ErrCapacityExceeded,
		)
	}
	if capacity.ItemTargets > policy.MaxItemTargets ||
		capacity.GroupTargets > policy.MaxGroupTargets {
		return Capacity{}, capacityExceeded(capacity, policy)
	}

	return capacity, nil
}

func checkedMultiply(left, right int64) (int64, bool) {
	if left <= 0 || right <= 0 || left > math.MaxInt64/right {
		return 0, false
	}
	return left * right, true
}

func capacityExceeded(
	capacity Capacity,
	policy CapacityPolicy,
) error {
	return fmt.Errorf(
		"%w: items=%d/%d groups=%d/%d targets=%d/%d item_targets=%d/%d group_targets=%d/%d",
		ErrCapacityExceeded,
		capacity.Items,
		policy.MaxItems,
		capacity.Groups,
		policy.MaxGroups,
		capacity.Targets,
		policy.MaxTargets,
		capacity.ItemTargets,
		policy.MaxItemTargets,
		capacity.GroupTargets,
		policy.MaxGroupTargets,
	)
}
