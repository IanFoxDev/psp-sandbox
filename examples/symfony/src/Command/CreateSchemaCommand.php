<?php

namespace App\Command;

use Doctrine\DBAL\Connection;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;

#[AsCommand('app:schema', 'Create the tables of the example (PostgreSQL)')]
final class CreateSchemaCommand extends Command
{
    public function __construct(private readonly Connection $db)
    {
        parent::__construct();
    }

    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        $this->db->executeStatement(<<<'SQL'
            CREATE TABLE IF NOT EXISTS orders (
                id BIGSERIAL PRIMARY KEY,
                reference VARCHAR(64) NOT NULL UNIQUE,
                amount BIGINT NOT NULL,
                currency VARCHAR(5) NOT NULL,
                status VARCHAR(16) NOT NULL DEFAULT 'pending',
                psp_payment_id VARCHAR(64) UNIQUE,
                -- What the shop has credited to the customer. Must never exceed amount.
                credited_amount BIGINT NOT NULL DEFAULT 0
            )
            SQL);
        // Ids of provider events already applied. The primary key is the deduplication.
        $this->db->executeStatement(<<<'SQL'
            CREATE TABLE IF NOT EXISTS psp_events (
                event_id VARCHAR(64) PRIMARY KEY,
                type VARCHAR(64) NOT NULL,
                received_at TIMESTAMPTZ NOT NULL DEFAULT now()
            )
            SQL);
        $output->writeln('schema ready');

        return Command::SUCCESS;
    }
}
