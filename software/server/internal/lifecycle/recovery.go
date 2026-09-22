package lifecycle

import (
	"context"

	"github.com/kitpro/kitpro/software/server/internal/containers"
)

// RecoverInterrupted performs observation-only classification and the one
// conservative automatic action supported by this slice: forward-commit an
// exact candidate that is already running, or finalize an already committed
// generation. Ambiguous and pre-start cutovers remain action_required.
func RecoverInterrupted(ctx context.Context, store Store, runtime containers.LifecycleRuntime) (int, error) {
	rows, err := store.DB.QueryContext(ctx, generationSelect+` JOIN helper_operations o ON o.operation_id=g.creating_operation_id WHERE o.state='action_required' AND g.status IN ('candidate','active') AND (SELECT COUNT(*) FROM runtime_components count_components WHERE count_components.installation_id=g.installation_id AND count_components.runtime_generation=g.runtime_generation)=1 ORDER BY g.created_at`)
	if err != nil {
		return 0, err
	}
	var generations []Generation
	for rows.Next() {
		g, e := scanGeneration(rows)
		if e != nil {
			rows.Close()
			return 0, e
		}
		generations = append(generations, g)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	recovered := 0
	runner := Runner{Runtime: runtime, Store: store}
	for _, g := range generations {
		result := Result{Generation: g.Generation, ReleaseID: g.ReleaseID, RuntimeState: "running", ContainerName: g.Component.ContainerName, ContainerID: g.Component.ContainerID, NetworkName: g.NetworkName, Bindings: g.Bindings}
		if g.Status == "active" {
			observed, observeErr := runner.verifyStoredGeneration(ctx, g, containers.RuntimeRunning)
			if observeErr != nil {
				continue
			}
			network, networkErr := runtime.ObserveNetwork(ctx, g.NetworkName)
			if networkErr != nil || !network.Exists || network.ID != g.NetworkID {
				continue
			}
			attachment, ok := observed.Networks[g.NetworkName]
			if !ok || attachment.NetworkID != network.ID {
				continue
			}
			if err = store.FinalizeCommittedRecovery(ctx, g, result); err != nil {
				return recovered, err
			}
			recovered++
			continue
		}
		observed, e := runner.verifyStoredGeneration(ctx, g, containers.RuntimeRunning)
		if e != nil {
			continue
		}
		network, e := runtime.ObserveNetwork(ctx, g.NetworkName)
		if e != nil || !network.Exists || network.ID != g.NetworkID {
			continue
		}
		attachment, ok := observed.Networks[g.NetworkName]
		if !ok || attachment.NetworkID != network.ID {
			continue
		}
		if err = store.RecoverForward(ctx, g, result); err != nil {
			return recovered, err
		}
		recovered++
	}
	multiRecovered, err := recoverInterruptedMulti(ctx, store, runtime)
	if err != nil {
		return recovered, err
	}
	recovered += multiRecovered
	return recovered, nil
}

func recoverInterruptedMulti(ctx context.Context, store Store, runtime containers.LifecycleRuntime) (int, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT g.installation_id,g.runtime_generation FROM runtime_generations g JOIN helper_operations o ON o.operation_id=g.creating_operation_id WHERE o.state='action_required' AND g.status IN ('candidate','active') AND (SELECT COUNT(*) FROM runtime_components c WHERE c.installation_id=g.installation_id AND c.runtime_generation=g.runtime_generation)>1 ORDER BY g.created_at`)
	if err != nil {
		return 0, err
	}
	type key struct {
		installation string
		generation   int
	}
	var keys []key
	for rows.Next() {
		var item key
		if err = rows.Scan(&item.installation, &item.generation); err != nil {
			rows.Close()
			return 0, err
		}
		keys = append(keys, item)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	recovered := 0
	runner := MultiRunner{Runtime: runtime, Store: store}
	for _, item := range keys {
		generation, loadErr := store.LoadMultiGeneration(ctx, item.installation, item.generation)
		if loadErr != nil || runner.verifyGeneration(ctx, generation, containers.RuntimeRunning) != nil {
			continue
		}
		result := Result{Generation: generation.Generation, ReleaseID: generation.ReleaseID, RuntimeState: "running", NetworkName: generation.NetworkName, Bindings: generation.Bindings, Components: map[string]string{}}
		for _, component := range generation.Components {
			result.Components[component.ID] = component.ContainerID
		}
		if generation.Status == "active" {
			if err = store.FinalizeCommittedRecovery(ctx, Generation{InstallationID: generation.InstallationID, Generation: generation.Generation, CreatingOperationID: generation.CreatingOperationID}, result); err != nil {
				return recovered, err
			}
		} else {
			if generation.Generation > 1 {
				previous, previousErr := store.LoadMultiGeneration(ctx, generation.InstallationID, generation.Generation-1)
				if previousErr != nil || runner.verifyGeneration(ctx, previous, containers.RuntimeStopped) != nil {
					continue
				}
			}
			if err = store.RecordRecoveredMultiVerification(ctx, generation); err != nil {
				continue
			}
			if err = store.RecoverForwardMulti(ctx, generation, result); err != nil {
				return recovered, err
			}
		}
		recovered++
	}
	return recovered, nil
}
