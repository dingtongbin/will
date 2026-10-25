use std::hint::black_box;
use std::time::Instant;

use crossbeam_channel::{bounded, select};

fn percentile(samples: &mut [u128], num: usize, den: usize) -> u128 {
    samples.sort_unstable();
    let index = (samples.len() - 1) * num / den;
    samples[index]
}

fn report(name: &str, unit: &str, mut samples: Vec<u128>, count: u64) {
    let p50 = percentile(&mut samples, 50, 100);
    let p95 = percentile(&mut samples, 95, 100);
    let p99 = percentile(&mut samples, 99, 100);
    let p999 = percentile(&mut samples, 999, 1000);
    println!("{name} p50 {p50} ns");
    println!("{name} p95 {p95} ns");
    println!("{name} p99 {p99} ns");
    println!("{name} p999 {p999} ns");
    println!("{name} {unit} {count}");
}

fn report_throughput(name: &str, count: u64, elapsed_ns: u128) {
    let cores = std::thread::available_parallelism()
        .map(|value| value.get() as u128)
        .unwrap_or(1);
    let ops_per_sec = (count as u128) * 1_000_000_000u128 / elapsed_ns.max(1);
    println!("{name} ops_per_sec {ops_per_sec}");
    println!("{name} ops_per_core_sec {}", ops_per_sec / cores);
}

fn chan() {
    const COUNT: u64 = 1_000_000;
    const REPS: usize = 5;
    let (sender, receiver) = bounded::<i64>(64);
    let mut warm = 0i64;
    for i in 0..1000i64 {
        sender.send(i).unwrap();
        warm = warm.wrapping_add(receiver.recv().unwrap());
    }
    black_box(warm);

    let mut samples = Vec::with_capacity(REPS);
    let mut checksum = 0i64;
    for _ in 0..REPS {
        let start = Instant::now();
        let mut total = 0i64;
        for i in 0..COUNT as i64 {
            sender.send(i).unwrap();
            total = total.wrapping_add(receiver.recv().unwrap());
        }
        samples.push(start.elapsed().as_nanos());
        checksum = checksum.wrapping_add(total);
        black_box(checksum);
    }
    report("chan", "messages", samples, COUNT);
}

fn choose() {
    const COUNT: u64 = 500_000;
    const REPS: usize = 5;
    let (first_tx, first_rx) = bounded::<i64>(1);
    let (_second_tx, second_rx) = bounded::<i64>(1);
    let (_third_tx, third_rx) = bounded::<i64>(1);
    let (_fourth_tx, fourth_rx) = bounded::<i64>(1);

    let mut warm = 0i64;
    for i in 0..1000i64 {
        first_tx.send(i).unwrap();
        select! {
            recv(first_rx) -> value => { if let Ok(v) = value { warm = warm.wrapping_add(v); } }
            recv(second_rx) -> _ => {}
            recv(third_rx) -> _ => {}
            recv(fourth_rx) -> _ => {}
            default => {}
        }
    }
    black_box(warm);

    let mut samples = Vec::with_capacity(REPS);
    let mut checksum = 0i64;
    for _ in 0..REPS {
        let start = Instant::now();
        let mut total = 0i64;
        for i in 0..COUNT as i64 {
            first_tx.send(i).unwrap();
            select! {
                recv(first_rx) -> value => { if let Ok(v) = value { total = total.wrapping_add(v); } }
                recv(second_rx) -> _ => {}
                recv(third_rx) -> _ => {}
                recv(fourth_rx) -> _ => {}
                default => {}
            }
        }
        samples.push(start.elapsed().as_nanos());
        checksum = checksum.wrapping_add(total);
        black_box(checksum);
    }
    report("select", "waits", samples, COUNT);
}

fn tail() {
    const COUNT: u64 = 100_000;
    const REPS: usize = 5;
    let (sender, receiver) = bounded::<i64>(64);
    let mut warm = 0i64;
    for i in 0..1000i64 {
        sender.send(i).unwrap();
        warm = warm.wrapping_add(receiver.recv().unwrap());
    }
    black_box(warm);

    let started = Instant::now();
    let mut samples = Vec::with_capacity(REPS * COUNT as usize);
    let mut checksum = 0i64;
    for _ in 0..REPS {
        for i in 0..COUNT as i64 {
            let start = Instant::now();
            sender.send(i).unwrap();
            checksum = checksum.wrapping_add(receiver.recv().unwrap());
            samples.push(start.elapsed().as_nanos());
        }
        black_box(checksum);
    }
    let elapsed = started.elapsed().as_nanos();
    report("tail", "round-trips", samples, REPS as u64 * COUNT);
    report_throughput("tail", REPS as u64 * COUNT, elapsed);
}

fn select_tail() {
    const COUNT: u64 = 100_000;
    const REPS: usize = 5;
    let (first_tx, first_rx) = bounded::<i64>(1);
    let (_second_tx, second_rx) = bounded::<i64>(1);
    let (_third_tx, third_rx) = bounded::<i64>(1);
    let (_fourth_tx, fourth_rx) = bounded::<i64>(1);
    let mut warm = 0i64;
    for i in 0..1000i64 {
        first_tx.send(i).unwrap();
        select! {
            recv(first_rx) -> value => { if let Ok(v) = value { warm = warm.wrapping_add(v); } }
            recv(second_rx) -> _ => {}
            recv(third_rx) -> _ => {}
            recv(fourth_rx) -> _ => {}
            default => {}
        }
    }
    black_box(warm);

    let started = Instant::now();
    let mut samples = Vec::with_capacity(REPS * COUNT as usize);
    let mut checksum = 0i64;
    for _ in 0..REPS {
        for i in 0..COUNT as i64 {
            let start = Instant::now();
            first_tx.send(i).unwrap();
            select! {
                recv(first_rx) -> value => { if let Ok(v) = value { checksum = checksum.wrapping_add(v); } }
                recv(second_rx) -> _ => {}
                recv(third_rx) -> _ => {}
                recv(fourth_rx) -> _ => {}
                default => {}
            }
            samples.push(start.elapsed().as_nanos());
        }
        black_box(checksum);
    }
    let elapsed = started.elapsed().as_nanos();
    report("select-tail", "waits", samples, REPS as u64 * COUNT);
    report_throughput("select-tail", REPS as u64 * COUNT, elapsed);
}

fn main() {
    let which = std::env::args().nth(1).unwrap_or_else(|| "chan".to_owned());
    match which.as_str() {
        "chan" => chan(),
        "select" => choose(),
        "tail" => tail(),
        "select-tail" => select_tail(),
        other => panic!("unknown scenario: {other}"),
    }
}
