use std::hint::black_box;
use std::sync::{Arc, Barrier};
use std::thread;
use std::time::Instant;

use compio::runtime::Runtime;

fn work(index: i32) -> i32 {
    let mut acc = 0i32;
    for i in 0..100 {
        acc += i;
    }
    acc + index
}

fn percentile(samples: &mut [u128], num: usize, den: usize) -> u128 {
    samples.sort_unstable();
    let index = (samples.len() - 1) * num / den;
    samples[index]
}

fn main() {
    const COUNT: usize = 10_000;
    const REPS: usize = 5;
    let cores = std::thread::available_parallelism()
        .map(|value| value.get())
        .unwrap_or(1);
    let per_worker = COUNT / cores.max(1);
    let total = per_worker * cores.max(1);
    let mut samples = Vec::with_capacity(REPS * total);
    let mut rounds = Vec::with_capacity(REPS);

    for _ in 0..REPS {
        let barrier = Arc::new(Barrier::new(cores + 1));
        let mut workers = Vec::with_capacity(cores);
        for worker in 0..cores {
            let barrier = Arc::clone(&barrier);
            workers.push(thread::spawn(move || {
                let runtime = Runtime::new().expect("compio runtime");
                barrier.wait();
                runtime.block_on(async {
                    let mut handles = Vec::with_capacity(per_worker);
                    for offset in 0..per_worker {
                        let index = (worker * per_worker + offset) as i32;
                        let start = Instant::now();
                        handles.push(runtime.spawn(async move {
                            black_box(work(index));
                            start.elapsed().as_nanos()
                        }));
                    }
                    let mut durations = Vec::with_capacity(per_worker);
                    for handle in handles {
                        durations.push(handle.await.expect("compio task"));
                    }
                    durations
                })
            }));
        }
        let started = Instant::now();
        barrier.wait();
        let mut round = Vec::with_capacity(total);
        for worker in workers {
            round.extend(worker.join().expect("compio worker"));
        }
        rounds.push(started.elapsed().as_nanos());
        samples.extend(round);
    }

    let p50 = percentile(&mut samples, 50, 100);
    let p99 = percentile(&mut samples, 99, 100);
    let p999 = percentile(&mut samples, 999, 1000);
    let elapsed: u128 = rounds.iter().sum();
    let ops_per_sec = (REPS * total) as u128 * 1_000_000_000u128 / elapsed.max(1);
    println!("compio-spawn p50 {p50} ns");
    println!("compio-spawn p99 {p99} ns");
    println!("compio-spawn p999 {p999} ns");
    println!("compio-spawn samples {}", REPS * total);
    println!("compio-spawn ops_per_sec {ops_per_sec}");
    println!("compio-spawn ops_per_core_sec {}", ops_per_sec / cores as u128);
}
