#!/usr/bin/env node
import { Command } from 'commander'
import { printDevInfo } from './commands/dev.js'
import { runDoctor } from './commands/doctor.js'
import { runSetup } from './commands/setup.js'
import { writeDevSeed } from './commands/seed.js'

const program = new Command()

program
  .name('parsar')
  .description('Parsar local runner and development harness CLI')
  .version('0.0.0')

program.command('setup').description('Create ~/.parsar local directories').action(runSetup)
program.command('dev').description('Print Phase 0 development endpoints').action(printDevInfo)
program.command('doctor').description('Print diagnostic paths without touching CWD').action(runDoctor)
program.command('seed-dev').description('Write deterministic dev seed under ~/.parsar/dev/seed').action(writeDevSeed)

program.parseAsync(process.argv)
